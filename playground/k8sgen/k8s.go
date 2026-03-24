package k8sgen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/flashbots/builder-playground/utils"
	"gopkg.in/yaml.v2"
)

// GenerateK8s generates Kubernetes manifests from the session's docker-compose.yaml
// using kompose, patches any missing volume mounts, and writes a minikube-mount.sh
// script for all host-path volumes.
func GenerateK8s(sessionDir string) error {
	composeFile := filepath.Join(sessionDir, "docker-compose.yaml")
	k8sDir := filepath.Join(sessionDir, "k8s")

	if err := os.MkdirAll(k8sDir, 0o755); err != nil {
		return fmt.Errorf("failed to create k8s dir: %w", err)
	}

	// Run kompose convert
	cmd := exec.Command("kompose", "convert",
		"-f", composeFile,
		"--volumes", "hostPath",
		"-o", k8sDir,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kompose convert failed: %w\n%s", err, errBuf.String())
	}

	// Parse compose volumes per service
	serviceVolumes, err := parseComposeVolumesPerService(composeFile)
	if err != nil {
		return err
	}

	// Parse named Docker volumes (e.g. "volume-beacon-data") — kompose incorrectly
	// assigns them a hostPath; we replace them with emptyDir.
	namedVolumes, err := parseNamedDockerVolumes(composeFile)
	if err != nil {
		return err
	}

	// Normalize the session dir container path to /data across all services so
	// that fixMountVolumes and patchK8sMissingVolumes both agree on the mount path.
	for svc := range serviceVolumes {
		for i := range serviceVolumes[svc] {
			if serviceVolumes[svc][i].hostPath == sessionDir {
				serviceVolumes[svc][i].containerPath = "/data"
			}
		}
	}

	// Fix hostPath volumes kompose generated from bind mounts:
	//   - file paths           → remove volume and its mounts
	//   - named Docker volumes → replace with emptyDir
	if err := fixMountVolumes(k8sDir, sessionDir, namedVolumes); err != nil {
		return fmt.Errorf("failed to fix hostPath volumes: %w", err)
	}

	// Patch k8s Deployment files with any bind mounts that kompose dropped
	if err := patchK8sMissingVolumes(k8sDir, serviceVolumes); err != nil {
		return fmt.Errorf("failed to patch k8s files: %w", err)
	}

	// Collect mount paths for minikube-mount.sh: collapse everything under the
	// session dir into a single mount; keep any paths outside it as-is.
	mountPaths := map[string]struct{}{}
	for _, mounts := range serviceVolumes {
		for _, m := range mounts {
			if strings.HasPrefix(m.hostPath, utils.TempPlaygroundDirPath()+"/") {
				continue // converted to emptyDir, no minikube mount needed
			}
			if m.hostPath == sessionDir || strings.HasPrefix(m.hostPath, sessionDir+"/") {
				mountPaths[sessionDir] = struct{}{}
			} else {
				mountPaths[m.hostPath] = struct{}{}
			}
		}
	}

	// Write minikube-mount.sh
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n\n")
	sb.WriteString("# Mount all host paths into minikube\n")
	for hostPath := range mountPaths {
		sb.WriteString(fmt.Sprintf("minikube mount %s:%s &\n", hostPath, hostPath))
	}
	sb.WriteString("\nwait\n")

	mountScript := filepath.Join(k8sDir, "minikube-mount.sh")
	if err := os.WriteFile(mountScript, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("failed to write minikube-mount.sh: %w", err)
	}

	return nil
}

type bindMount struct {
	hostPath      string
	containerPath string
}

// parseNamedDockerVolumes returns the set of named Docker volume names defined
// in the top-level "volumes:" section of the compose file.
func parseNamedDockerVolumes(composeFile string) (map[string]struct{}, error) {
	data, err := os.ReadFile(composeFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}
	var compose map[interface{}]interface{}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return nil, fmt.Errorf("failed to parse compose file: %w", err)
	}
	result := map[string]struct{}{}
	volumes, _ := compose["volumes"].(map[interface{}]interface{})
	for nameRaw := range volumes {
		result[fmt.Sprintf("%v", nameRaw)] = struct{}{}
	}
	return result, nil
}

func parseComposeVolumesPerService(composeFile string) (map[string][]bindMount, error) {
	data, err := os.ReadFile(composeFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	var compose map[interface{}]interface{}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return nil, fmt.Errorf("failed to parse compose file: %w", err)
	}

	result := map[string][]bindMount{}
	services, _ := compose["services"].(map[interface{}]interface{})
	for nameRaw, svc := range services {
		name := fmt.Sprintf("%v", nameRaw)
		svcMap, _ := svc.(map[interface{}]interface{})
		if svcMap == nil {
			continue
		}
		for _, vol := range toStringSlice(svcMap["volumes"]) {
			parts := strings.SplitN(vol, ":", 2)
			if len(parts) != 2 || !strings.HasPrefix(parts[0], "/") {
				continue
			}
			result[name] = append(result[name], bindMount{
				hostPath:      parts[0],
				containerPath: parts[1],
			})
		}
	}
	return result, nil
}

// fixMountVolumes fixes hostPath volumes kompose generated from bind mounts:
// file-path volumes are removed (along with their volumeMounts), temp-dir
// volumes are replaced with emptyDir, and named Docker volumes that kompose
// incorrectly assigned a hostPath are also replaced with emptyDir.
func fixMountVolumes(k8sDir, sessionDir string, namedVolumes map[string]struct{}) error {
	entries, err := os.ReadDir(k8sDir)
	if err != nil {
		return fmt.Errorf("failed to read k8s dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		filePath := filepath.Join(k8sDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		var doc map[interface{}]interface{}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return err
		}
		if kind, _ := doc["kind"].(string); kind != "Deployment" {
			continue
		}
		podSpec := nestedIMap(doc, "spec", "template", "spec")
		if podSpec == nil {
			continue
		}
		removeVols := map[string]struct{}{}
		dirty := false
		for _, vol := range toSlice(podSpec["volumes"]) {
			volMap, _ := vol.(map[interface{}]interface{})
			hp, _ := volMap["hostPath"].(map[interface{}]interface{})
			if hp == nil {
				continue
			}
			volName, _ := volMap["name"].(string)
			path, _ := hp["path"].(string)
			info, statErr := os.Stat(path)
			if statErr == nil && !info.IsDir() {
				removeVols[volName] = struct{}{}
				dirty = true
			} else if _, ok := namedVolumes[volName]; ok || strings.HasPrefix(path, utils.TempPlaygroundDirPath()+"/") {
				delete(volMap, "hostPath")
				volMap["emptyDir"] = map[interface{}]interface{}{}
				dirty = true
			} else if path == sessionDir {
				for _, c := range toSlice(podSpec["containers"]) {
					cMap, _ := c.(map[interface{}]interface{})
					for _, vm := range toSlice(cMap["volumeMounts"]) {
						vmMap, _ := vm.(map[interface{}]interface{})
						if vmMap["name"] == volName {
							vmMap["mountPath"] = "/data"
						}
					}
				}
				dirty = true
			}
		}
		if !dirty {
			continue
		}

		if len(removeVols) > 0 {
			newVols := make([]interface{}, 0)
			for _, vol := range toSlice(podSpec["volumes"]) {
				volMap, _ := vol.(map[interface{}]interface{})
				if name, _ := volMap["name"].(string); name != "" {
					if _, drop := removeVols[name]; drop {
						continue
					}
				}
				newVols = append(newVols, vol)
			}
			podSpec["volumes"] = newVols

			for _, c := range toSlice(podSpec["containers"]) {
				cMap, _ := c.(map[interface{}]interface{})
				newVMs := make([]interface{}, 0)
				for _, vm := range toSlice(cMap["volumeMounts"]) {
					vmMap, _ := vm.(map[interface{}]interface{})
					if name, _ := vmMap["name"].(string); name != "" {
						if _, drop := removeVols[name]; drop {
							continue
						}
					}
					newVMs = append(newVMs, vm)
				}
				cMap["volumeMounts"] = newVMs
			}
		}

		out, err := yaml.Marshal(doc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filePath, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// patchK8sMissingVolumes walks k8sDir and for each Deployment file adds any bind
// mounts from the compose file that kompose did not include.
func patchK8sMissingVolumes(k8sDir string, serviceVolumes map[string][]bindMount) error {
	entries, err := os.ReadDir(k8sDir)
	if err != nil {
		return fmt.Errorf("failed to read k8s dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		if err := patchDeploymentIfNeeded(filepath.Join(k8sDir, entry.Name()), serviceVolumes); err != nil {
			return fmt.Errorf("patching %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func patchDeploymentIfNeeded(filePath string, serviceVolumes map[string][]bindMount) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var doc map[interface{}]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	if kind, _ := doc["kind"].(string); kind != "Deployment" {
		return nil
	}

	metadata, _ := doc["metadata"].(map[interface{}]interface{})
	if metadata == nil {
		return nil
	}
	svcName, _ := metadata["name"].(string)
	mounts, ok := serviceVolumes[svcName]
	if !ok {
		return nil
	}

	podSpec := nestedIMap(doc, "spec", "template", "spec")
	if podSpec == nil {
		return nil
	}

	containers, _ := podSpec["containers"].([]interface{})

	// Collect mountPaths already present across all containers
	existing := map[string]struct{}{}
	for _, c := range containers {
		cMap, _ := c.(map[interface{}]interface{})
		for _, vm := range toSlice(cMap["volumeMounts"]) {
			vmMap, _ := vm.(map[interface{}]interface{})
			if mp, _ := vmMap["mountPath"].(string); mp != "" {
				existing[mp] = struct{}{}
			}
		}
	}

	// Find compose bind mounts not yet in the k8s file, skipping file-level mounts
	var missing []bindMount
	for _, m := range mounts {
		if _, found := existing[m.containerPath]; found {
			continue
		}
		if info, err := os.Stat(m.hostPath); err == nil && !info.IsDir() {
			continue
		}
		missing = append(missing, m)
	}
	if len(missing) == 0 {
		return nil
	}

	// Add missing volumes + volumeMounts
	volumes := toSlice(podSpec["volumes"])
	for i, m := range missing {
		volName := fmt.Sprintf("%s-extra-%d", svcName, i)
		volumes = append(volumes, map[interface{}]interface{}{
			"name": volName,
			"hostPath": map[interface{}]interface{}{
				"path": m.hostPath,
			},
		})
		for _, c := range containers {
			cMap, _ := c.(map[interface{}]interface{})
			vms := toSlice(cMap["volumeMounts"])
			cMap["volumeMounts"] = append(vms, map[interface{}]interface{}{
				"name":      volName,
				"mountPath": m.containerPath,
			})
		}
	}
	podSpec["volumes"] = volumes

	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal patched deployment: %w", err)
	}
	return os.WriteFile(filePath, out, 0o644)
}

// nestedIMap navigates a chain of keys in a map[interface{}]interface{} tree.
func nestedIMap(m map[interface{}]interface{}, keys ...string) map[interface{}]interface{} {
	cur := m
	for _, k := range keys {
		next, _ := cur[k].(map[interface{}]interface{})
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func toSlice(v interface{}) []interface{} {
	s, _ := v.([]interface{})
	return s
}

func toStringSlice(v interface{}) []string {
	sl, _ := v.([]interface{})
	out := make([]string, 0, len(sl))
	for _, item := range sl {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
