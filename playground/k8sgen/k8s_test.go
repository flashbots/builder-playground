package k8sgen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

// writeFile writes content to a file in dir and returns the path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

// readDeployment reads a Deployment YAML from a file.
func readDeployment(t *testing.T, path string) map[interface{}]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[interface{}]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}

func TestParseNamedDockerVolumes(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	compose := writeFile(t, dir, "docker-compose.yaml", `
services:
  el:
    image: foo
    volumes:
      - volume-el-data:/data
      - /host/path:/config

volumes:
  volume-el-data:
  volume-beacon-data:
`)
	got, err := parseNamedDockerVolumes(compose)
	r.NoError(err)
	r.Contains(got, "volume-el-data")
	r.Contains(got, "volume-beacon-data")
	r.Len(got, 2)
}

func TestParseNamedDockerVolumes_NoVolumesSection(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	compose := writeFile(t, dir, "docker-compose.yaml", `
services:
  el:
    image: foo
`)
	got, err := parseNamedDockerVolumes(compose)
	r.NoError(err)
	r.Empty(got)
}

func TestParseComposeVolumesPerService(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	compose := writeFile(t, dir, "docker-compose.yaml", `
services:
  el:
    image: foo
    volumes:
      - /host/data:/data
      - volume-el-data:/internal
      - /host/config:/config
  beacon:
    image: bar
    volumes:
      - volume-beacon-data:/data_beacon
`)
	got, err := parseComposeVolumesPerService(compose)
	r.NoError(err)

	elMounts := got["el"]
	r.Len(elMounts, 2)
	r.Equal(bindMount{hostPath: "/host/data", containerPath: "/data"}, elMounts[0])
	r.Equal(bindMount{hostPath: "/host/config", containerPath: "/config"}, elMounts[1])

	// Named volume should be skipped
	r.Empty(got["beacon"])
}

func TestFixMountVolumes_NamedVolumeBecomesEmptyDir(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	writeFile(t, k8sDir, "beacon-deployment.yaml", `
kind: Deployment
metadata:
  name: beacon
spec:
  template:
    spec:
      containers:
        - name: beacon
          volumeMounts:
            - name: volume-beacon-data
              mountPath: /data_beacon
      volumes:
        - name: volume-beacon-data
          hostPath:
            path: /some/session/dir
`)
	r.NoError(fixMountVolumes(k8sDir, "", map[string]struct{}{"volume-beacon-data": {}}))

	doc := readDeployment(t, filepath.Join(k8sDir, "beacon-deployment.yaml"))
	podSpec := nestedIMap(doc, "spec", "template", "spec")
	for _, vol := range toSlice(podSpec["volumes"]) {
		volMap, _ := vol.(map[interface{}]interface{})
		if volMap["name"] == "volume-beacon-data" {
			r.Contains(volMap, "emptyDir", "expected emptyDir for volume-beacon-data")
			r.NotContains(volMap, "hostPath", "hostPath should be removed")
		}
	}
}

func TestFixMountVolumes_TempPathBecomesEmptyDir(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	tempPath := filepath.Join(t.TempDir(), "bind-mount-volumes", "volume-el-data")
	r.NoError(os.MkdirAll(tempPath, 0o755))

	writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts:
            - name: el-hostpath3
              mountPath: /data
      volumes:
        - name: el-hostpath3
          hostPath:
            path: `+tempPath+`
`)
	// Use the named-volume mechanism to cover the emptyDir replacement path.
	r.NoError(fixMountVolumes(k8sDir, "", map[string]struct{}{"el-hostpath3": {}}))

	doc := readDeployment(t, filepath.Join(k8sDir, "el-deployment.yaml"))
	podSpec := nestedIMap(doc, "spec", "template", "spec")
	for _, vol := range toSlice(podSpec["volumes"]) {
		volMap, _ := vol.(map[interface{}]interface{})
		if volMap["name"] == "el-hostpath3" {
			r.Contains(volMap, "emptyDir")
		}
	}
}

func TestFixMountVolumes_FileHostPathRemoved(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	tmpFile, err := os.CreateTemp(t.TempDir(), "secret")
	r.NoError(err)
	tmpFile.Close()

	writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts:
            - name: el-file-vol
              mountPath: /config/jwt
            - name: el-data
              mountPath: /data
      volumes:
        - name: el-file-vol
          hostPath:
            path: `+tmpFile.Name()+`
        - name: el-data
          hostPath:
            path: /some/dir
`)
	r.NoError(fixMountVolumes(k8sDir, "", map[string]struct{}{}))

	doc := readDeployment(t, filepath.Join(k8sDir, "el-deployment.yaml"))
	podSpec := nestedIMap(doc, "spec", "template", "spec")

	for _, vol := range toSlice(podSpec["volumes"]) {
		volMap, _ := vol.(map[interface{}]interface{})
		r.NotEqual("el-file-vol", volMap["name"], "file-level volume should have been removed")
	}
	for _, c := range toSlice(podSpec["containers"]) {
		cMap, _ := c.(map[interface{}]interface{})
		for _, vm := range toSlice(cMap["volumeMounts"]) {
			vmMap, _ := vm.(map[interface{}]interface{})
			r.NotEqual("el-file-vol", vmMap["name"], "volumeMount for file-level volume should have been removed")
		}
	}
}

func TestFixMountVolumes_SessionDirMountPathPatchedToData(t *testing.T) {
	r := require.New(t)
	sessionDir := t.TempDir()
	k8sDir := t.TempDir()
	writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts:
            - name: el-session
              mountPath: /artifacts
      volumes:
        - name: el-session
          hostPath:
            path: `+sessionDir+`
`)
	r.NoError(fixMountVolumes(k8sDir, sessionDir, map[string]struct{}{}))

	doc := readDeployment(t, filepath.Join(k8sDir, "el-deployment.yaml"))
	podSpec := nestedIMap(doc, "spec", "template", "spec")
	for _, c := range toSlice(podSpec["containers"]) {
		cMap, _ := c.(map[interface{}]interface{})
		for _, vm := range toSlice(cMap["volumeMounts"]) {
			vmMap, _ := vm.(map[interface{}]interface{})
			if vmMap["name"] == "el-session" {
				r.Equal("/data", vmMap["mountPath"])
			}
		}
	}
}

func TestFixMountVolumes_NonDeploymentSkipped(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	original := `kind: Service
metadata:
  name: el
spec:
  ports:
    - port: 8551
`
	writeFile(t, k8sDir, "el-service.yaml", original)
	r.NoError(fixMountVolumes(k8sDir, "", map[string]struct{}{}))
	got, err := os.ReadFile(filepath.Join(k8sDir, "el-service.yaml"))
	r.NoError(err)
	r.Equal(original, string(got))
}

func TestPatchDeploymentIfNeeded_AddsMissingMount(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	hostDir := t.TempDir()

	deployPath := writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts: []
      volumes: []
`)
	serviceVolumes := map[string][]bindMount{
		"el": {{hostPath: hostDir, containerPath: "/data"}},
	}
	r.NoError(patchDeploymentIfNeeded(deployPath, serviceVolumes))

	doc := readDeployment(t, deployPath)
	podSpec := nestedIMap(doc, "spec", "template", "spec")

	var found bool
	for _, vol := range toSlice(podSpec["volumes"]) {
		volMap, _ := vol.(map[interface{}]interface{})
		hp, _ := volMap["hostPath"].(map[interface{}]interface{})
		if hp != nil && hp["path"] == hostDir {
			found = true
		}
	}
	r.True(found, "expected missing bind mount to be added as hostPath volume")
}

func TestPatchDeploymentIfNeeded_SkipsAlreadyPresent(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	hostDir := t.TempDir()

	deployPath := writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts:
            - name: existing-vol
              mountPath: /data
      volumes:
        - name: existing-vol
          hostPath:
            path: `+hostDir+`
`)
	serviceVolumes := map[string][]bindMount{
		"el": {{hostPath: hostDir, containerPath: "/data"}},
	}
	r.NoError(patchDeploymentIfNeeded(deployPath, serviceVolumes))

	doc := readDeployment(t, deployPath)
	podSpec := nestedIMap(doc, "spec", "template", "spec")
	r.Len(toSlice(podSpec["volumes"]), 1, "no new volumes should have been added")
}

func TestPatchDeploymentIfNeeded_SkipsFileMount(t *testing.T) {
	r := require.New(t)
	k8sDir := t.TempDir()
	tmpFile, err := os.CreateTemp(t.TempDir(), "jwt")
	r.NoError(err)
	tmpFile.Close()

	deployPath := writeFile(t, k8sDir, "el-deployment.yaml", `
kind: Deployment
metadata:
  name: el
spec:
  template:
    spec:
      containers:
        - name: el
          volumeMounts: []
      volumes: []
`)
	serviceVolumes := map[string][]bindMount{
		"el": {{hostPath: tmpFile.Name(), containerPath: "/config/jwt"}},
	}
	r.NoError(patchDeploymentIfNeeded(deployPath, serviceVolumes))

	doc := readDeployment(t, deployPath)
	podSpec := nestedIMap(doc, "spec", "template", "spec")
	r.Empty(toSlice(podSpec["volumes"]), "file-level mount should not be added")
}
