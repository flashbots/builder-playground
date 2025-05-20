package playground

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
	"gopkg.in/yaml.v2"
)

const defaultNetworkName = "ethplayground"

// LocalRunner is a component that runs the services from the manifest on the local host machine.
// By default, it uses docker and docker compose to run all the services.
// But, some services (if they are configured to do so) can be run on the host machine instead.
// When running inside docker, each service will use the port numbers they define in the component description.
// Besides, they will also bind to an available public port on the host machine.
// If the service runs on the host, it will use the host port numbers instead directly.
type LocalRunner struct {
	out      *output
	manifest *Manifest
	client   *client.Client

	// reservedPorts is a map of port numbers reserved for each service to avoid conflicts
	// since we reserve ports for all the services before they are used
	reservedPorts map[int]bool

	// overrides is a map of service name to the path of the executable to run
	// on the host machine instead of a container.
	overrides map[string]string

	// handles stores the references to the processes that are running on host machine
	// they are executed sequentially so we do not need to lock the handles
	handles []*exec.Cmd

	// exitError signals when one of the services fails
	exitErr chan error

	// signals whether we are running in interactive mode
	interactive bool

	// TODO: Merge instance with tasks
	instances []*instance

	// tasks tracks the status of each service
	tasksMtx     sync.Mutex
	tasks        map[string]*task
	taskUpdateCh chan struct{}

	// wether to bind the ports to the local interface
	bindHostPortsLocally bool

	// sessionID is a random sequence that is used to identify the session
	// it is used to identify the containers in the cleanup process
	sessionID string

	// networkName is the name of the network to use for the services
	networkName string

	// labels is the list of labels to apply to each resource being created
	labels map[string]string

	// logInternally outputs the logs of the service to the artifacts folder
	logInternally bool
}

type task struct {
	status string
	ready  bool
	logs   *os.File
}

var (
	taskStatusPending = "pending"
	taskStatusStarted = "started"
	taskStatusDie     = "die"
	taskStatusHealthy = "healthy"
)

type taskUI struct {
	tasks    map[string]string
	spinners map[string]spinner.Model
	style    lipgloss.Style
}

func newDockerClient() (*client.Client, error) {
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return client, nil
}

// TODO: add a runner config struct
func NewLocalRunner(out *output, manifest *Manifest, overrides map[string]string, interactive bool, bindHostPortsLocally bool, networkName string, labels map[string]string, logInternally bool) (*LocalRunner, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	// merge the overrides with the manifest overrides
	if overrides == nil {
		overrides = make(map[string]string)
	}
	for k, v := range manifest.overrides {
		overrides[k] = v
	}

	// Create the concrete instances to run
	instances := []*instance{}
	for _, service := range manifest.Services {
		component := FindComponent(service.ComponentName)
		if component == nil {
			return nil, fmt.Errorf("component not found '%s'", service.ComponentName)
		}
		instance := &instance{
			service:   service,
			component: component,
		}
		if logInternally {
			log_output, err := out.LogOutput(service.Name)
			if err != nil {
				return nil, fmt.Errorf("error getting log output: %w", err)
			}
			instance.logs = &serviceLogs{
				logRef: log_output,
				path:   log_output.Name(),
			}
		}
		instances = append(instances, instance)
	}

	// download any local release artifacts for the services that require them
	// TODO: it feels a bit weird to have all this logic on the new command. We should split it later on.
	for _, instance := range instances {
		ss := instance.service
		if ss.Labels[useHostExecutionLabel] == "true" {
			// If the service wants to run on the host, it must implement the ReleaseService interface
			// which provides functions to download the release artifact.
			releaseService, ok := instance.component.(ReleaseService)
			if !ok {
				return nil, fmt.Errorf("service '%s' must implement the ReleaseService interface", ss.Name)
			}
			releaseArtifact := releaseService.ReleaseArtifact()
			bin, err := DownloadRelease(out.homeDir, releaseArtifact)
			if err != nil {
				return nil, fmt.Errorf("failed to download release artifact for service '%s': %w", ss.Name, err)
			}
			overrides[ss.Name] = bin
		}
	}

	// Now, the override can either be one of two things (we are overloading the override map):
	// - docker image: In that case, change the manifest and remove from override map
	// - a path to an executable: In that case, we need to run it on the host machine
	// and use the override map <- We only check this case, and if it is not a path, we assume
	// it is a docker image. If it is not a docker image either, the error will be catched during the execution
	for k, v := range overrides {
		if _, err := os.Stat(v); err != nil {
			// this is a path to an executable, remove it from the overrides since we
			// assume it s a docker image and add it to manifest
			parts := strings.Split(v, ":")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid override docker image %s, expected image:tag", v)
			}

			srv := manifest.MustGetService(k)
			srv.Image = parts[0]
			srv.Tag = parts[1]

			delete(overrides, k)
			continue
		}
	}

	tasks := map[string]*task{}
	for _, svc := range manifest.Services {
		tasks[svc.Name] = &task{
			status: taskStatusPending,
			logs:   nil,
		}
	}

	if networkName == "" {
		networkName = defaultNetworkName
	}
	d := &LocalRunner{
		out:                  out,
		manifest:             manifest,
		client:               client,
		reservedPorts:        map[int]bool{},
		overrides:            overrides,
		handles:              []*exec.Cmd{},
		tasks:                tasks,
		taskUpdateCh:         make(chan struct{}),
		exitErr:              make(chan error, 2),
		bindHostPortsLocally: bindHostPortsLocally,
		sessionID:            uuid.New().String(),
		networkName:          networkName,
		instances:            instances,
		labels:               labels,
		logInternally:        logInternally,
	}

	if interactive {
		go d.printStatus()

		select {
		case d.taskUpdateCh <- struct{}{}:
		default:
		}
	}

	return d, nil
}

func (d *LocalRunner) Instances() []*instance {
	return d.instances
}

func (d *LocalRunner) printStatus() {
	fmt.Print("\033[s")
	lineOffset := 0

	// Get ordered service names from manifest
	orderedServices := make([]string, 0, len(d.manifest.Services))
	for _, svc := range d.manifest.Services {
		orderedServices = append(orderedServices, svc.Name)
	}

	// Initialize UI state
	ui := taskUI{
		tasks:    make(map[string]string),
		spinners: make(map[string]spinner.Model),
		style:    lipgloss.NewStyle(),
	}

	// Initialize spinners for each service
	for _, name := range orderedServices {
		sp := spinner.New()
		sp.Spinner = spinner.Dot
		ui.spinners[name] = sp
	}

	for {
		select {
		case <-d.taskUpdateCh:
			d.tasksMtx.Lock()

			// Clear the previous lines and move cursor up
			if lineOffset > 0 {
				fmt.Printf("\033[%dA", lineOffset)
				fmt.Print("\033[J")
			}

			lineOffset = 0
			// Use ordered services instead of ranging over map
			for _, name := range orderedServices {
				status := d.tasks[name].status
				var statusLine string

				switch status {
				case taskStatusStarted:
					sp := ui.spinners[name]
					sp.Tick()
					ui.spinners[name] = sp
					statusLine = ui.style.Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("%s [%s] Running", sp.View(), name))
				case taskStatusDie:
					statusLine = ui.style.Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("✗ [%s] Failed", name))
				case taskStatusPending:
					sp := ui.spinners[name]
					sp.Tick()
					ui.spinners[name] = sp
					statusLine = ui.style.Foreground(lipgloss.Color("3")).Render(fmt.Sprintf("%s [%s] Pending", sp.View(), name))
				}

				fmt.Println(statusLine)
				lineOffset++
			}

			d.tasksMtx.Unlock()
		}
	}
}

func (d *LocalRunner) AreReady() bool {
	d.tasksMtx.Lock()
	defer d.tasksMtx.Unlock()

	for name, task := range d.tasks {
		// ensure the task is not a host service
		if d.isHostService(name) {
			continue
		}

		// first ensure the task has started
		if task.status != taskStatusStarted {
			return false
		}

		// then ensure it is ready if it has a ready function
		svc := d.getService(name)
		if svc.ReadyCheck != nil {
			if !task.ready {
				return false
			}
		}
	}
	return true
}

func (d *LocalRunner) WaitForReady(ctx context.Context, timeout time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-time.After(1 * time.Second):
			if d.AreReady() {
				return nil
			}

		case err := <-d.exitErr:
			return err
		}
	}
}

func (d *LocalRunner) updateTaskStatus(name string, status string) {
	d.tasksMtx.Lock()
	defer d.tasksMtx.Unlock()
	if status == taskStatusHealthy {
		d.tasks[name].ready = true
	} else {
		d.tasks[name].status = status
	}

	if status == taskStatusDie {
		d.exitErr <- fmt.Errorf("container %s failed", name)
	}

	select {
	case d.taskUpdateCh <- struct{}{}:
	default:
	}
}

func (d *LocalRunner) ExitErr() <-chan error {
	return d.exitErr
}

func (d *LocalRunner) Stop() error {
	// only stop the containers that belong to this session
	containers, err := d.client.ContainerList(context.Background(), container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", fmt.Sprintf("playground.session=%s", d.sessionID))),
	})
	if err != nil {
		return fmt.Errorf("error getting container list: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(len(containers))

	var errCh chan error
	errCh = make(chan error, len(containers))

	for _, cont := range containers {
		go func(contID string) {
			defer wg.Done()
			if err := d.client.ContainerRemove(context.Background(), contID, container.RemoveOptions{
				RemoveVolumes: true,
				RemoveLinks:   false,
				Force:         true,
			}); err != nil {
				errCh <- fmt.Errorf("error removing container: %w", err)
			}
		}(cont.ID)
	}

	wg.Wait()

	// stop all the handles
	for _, handle := range d.handles {
		handle.Process.Kill()
	}

	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

// reservePort finds the first available port from the startPort and reserves it
// Note that we have to keep track of the port in 'reservedPorts' because
// the port allocation happens before the services uses it and binds to it.
func (d *LocalRunner) reservePort(startPort int, protocol string) int {
	for i := startPort; i < startPort+1000; i++ {
		if _, ok := d.reservedPorts[i]; ok {
			continue
		}

		bindAddr := "0.0.0.0"
		if d.bindHostPortsLocally {
			bindAddr = "127.0.0.1"
		}

		if protocol == ProtocolUDP {
			listener, err := net.ListenUDP("udp", &net.UDPAddr{
				Port: i,
				IP:   net.ParseIP(bindAddr),
			})
			if err != nil {
				continue
			}
			listener.Close()
		} else if protocol == ProtocolTCP {
			listener, err := net.Listen(protocol, fmt.Sprintf("%s:%d", bindAddr, i))
			if err != nil {
				continue
			}
			listener.Close()
		} else {
			panic(fmt.Sprintf("invalid protocol: %s", protocol))
		}

		d.reservedPorts[i] = true
		return i
	}
	panic("BUG: could not reserve a port")
}

func (d *LocalRunner) getService(name string) *Service {
	for _, svc := range d.manifest.Services {
		if svc.Name == name {
			return svc
		}
	}
	return nil
}

// applyTemplate resolves the templates from the manifest (Dir, Port, Connect) into
// the actual values for this specific docker execution.
func (d *LocalRunner) applyTemplate(s *Service) ([]string, map[string]string, error) {
	var input map[string]interface{}

	resolvePort := func(name string, defaultPort int, protocol string) int {
		// For {{Port "name" "defaultPort"}}:
		// - Service runs on host: return the host port
		// - Service runs inside docker: return the docker port
		if d.isHostService(s.Name) {
			return s.MustGetPort(name).HostPort
		}
		return defaultPort
	}

	funcs := template.FuncMap{
		"Service": func(name string, portLabel, protocol, user string) string {
			// For {{Service "name" "portLabel"}}:
			// - Service runs on host:
			//   A: target is inside docker: access with localhost:hostPort
			//   B: target is on the host: access with localhost:hostPort
			// - Service runs inside docker:
			//   C: target is inside docker: access it with DNS service:port
			//   D: target is on the host: access it with host.docker.internal:hostPort

			// find the service and the port that it resolves for that label
			svc := d.manifest.MustGetService(name)
			port := svc.MustGetPort(portLabel)

			if d.isHostService(s.Name) {
				// A and B
				return printAddr(protocol, "localhost", port.HostPort, user)
			} else {
				if d.isHostService(svc.Name) {
					// D
					return printAddr(protocol, "host.docker.internal", port.HostPort, user)
				}
				// C
				return printAddr(protocol, svc.Name, port.Port, user)
			}
		},
		"Port": func(name string, defaultPort int) int {
			return resolvePort(name, defaultPort, ProtocolTCP)
		},
		"PortUDP": func(name string, defaultPort int) int {
			return resolvePort(name, defaultPort, ProtocolUDP)
		},
	}

	runTemplate := func(arg string) (string, error) {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			return "", err
		}

		var out strings.Builder
		if err := tpl.Execute(&out, input); err != nil {
			return "", err
		}

		return out.String(), nil
	}

	// apply the templates to the arguments
	var argsResult []string
	for _, arg := range s.Args {
		newArg, err := runTemplate(arg)
		if err != nil {
			return nil, nil, err
		}
		argsResult = append(argsResult, newArg)
	}

	// apply the templates to the environment variables
	envs := map[string]string{}
	for k, v := range s.Env {
		newV, err := runTemplate(v)
		if err != nil {
			return nil, nil, err
		}
		envs[k] = newV
	}

	return argsResult, envs, nil
}

func printAddr(protocol, serviceName string, port int, user string) string {
	var protocolPrefix string
	if protocol != "" {
		protocolPrefix = protocol + "://"
	}

	if user != "" {
		return fmt.Sprintf("%s%s@%s:%s", protocolPrefix, user, serviceName, serviceName)
	}

	return fmt.Sprintf("%s%s:%d", protocolPrefix, serviceName, port)
}

func (d *LocalRunner) validateImageExists(imageName string) error {
	// Only check locally - don't attempt to pull
	_, err := d.client.ImageInspect(context.Background(), imageName)
	if err == nil {
		// Image exists locally
		return nil
	}

	// For all errors, assume docker-compose will handle pulling
	// This handles several cases:
	// 1. Private images that need authentication
	// 2. Images that will be pulled at runtime
	// 3. Network or timeout issues

	// We could return an error here, but it's better to let docker-compose handle
	// all image pulling since it respects Docker credentials and has better
	// handling for retries and timeouts

	// Log that we're assuming the image will be pulled by docker-compose
	log.Warn("Image not found locally. Assuming docker compose will pull it",
		"image", imageName)

	return nil
}

func (d *LocalRunner) toDockerComposeService(s *Service) (map[string]interface{}, error) {
	// apply the template again on the arguments to figure out the connections
	// at this point all of them are valid, we just have to resolve them again. We assume for now
	// everyone is going to be on docker at the same network.
	args, envs, err := d.applyTemplate(s)
	if err != nil {
		return nil, fmt.Errorf("failed to apply template, err: %w", err)
	}

	// The containers have access to the full set of artifacts on the /artifacts folder
	// so, we have to bind it as a volume on the container.
	outputFolder, err := d.out.AbsoluteDstPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for output folder: %w", err)
	}

	// Validate that the image exists
	imageName := fmt.Sprintf("%s:%s", s.Image, s.Tag)
	if err := d.validateImageExists(imageName); err != nil {
		return nil, fmt.Errorf("failed to validate image %s: %w", imageName, err)
	}

	labels := map[string]string{
		// It is important to use the playground label to identify the containers
		// during the cleanup process
		"playground":         "true",
		"playground.session": d.sessionID,
		"service":            s.Name,
	}

	// apply the user defined labels
	for k, v := range d.labels {
		labels[k] = v
	}

	// add the local ports exposed by the service as labels
	// we have to do this for now since we do not store the manifest in JSON yet.
	// Otherwise, we could use that directly
	for _, port := range s.Ports {
		labels[fmt.Sprintf("port.%s", port.Name)] = fmt.Sprintf("%d", port.Port)
	}

	// Use files mapped to figure out which files from the artifacts is using the service
	volumes := map[string]string{
		outputFolder: "/artifacts", // placeholder
	}
	for k, v := range s.FilesMapped {
		volumes[filepath.Join(outputFolder, v)] = k
	}

	// create the bind volumes
	for localPath, volumeName := range s.VolumesMapped {
		// If the volume name is an absolute path, use it directly
		if filepath.IsAbs(volumeName) {
			volumes[volumeName] = localPath
		} else {
			volumeDirAbsPath, err := d.createVolume(s.Name, volumeName)
			if err != nil {
				return nil, err
			}
			volumes[volumeDirAbsPath] = localPath
		}
	}

	volumesInLine := []string{}
	for k, v := range volumes {
		volumesInLine = append(volumesInLine, fmt.Sprintf("%s:%s", k, v))
	}

	// add the ports to the labels as well
	service := map[string]interface{}{
		"image":   imageName,
		"command": args,
		// Add volume mount for the output directory
		"volumes": volumesInLine,
		// Add the ethereum network
		"networks": []string{d.networkName},
		"labels":   labels,
		"restart":  "unless-stopped",
	}

	if s.Privileged {
		service["privileged"] = true
	}

	if len(envs) > 0 {
		service["environment"] = envs
	}

	if s.ReadyCheck != nil {
		var test []string
		if s.ReadyCheck.QueryURL != "" {
			// This is pretty much hardcoded for now.
			test = []string{"CMD-SHELL", "chmod +x /artifacts/scripts/query.sh && /artifacts/scripts/query.sh " + s.ReadyCheck.QueryURL}
		} else {
			test = s.ReadyCheck.Test
		}

		service["healthcheck"] = map[string]interface{}{
			"test":         test,
			"interval":     s.ReadyCheck.Interval.String(),
			"timeout":      s.ReadyCheck.Timeout.String(),
			"retries":      s.ReadyCheck.Retries,
			"start_period": s.ReadyCheck.StartPeriod.String(),
		}
	}

	if s.DependsOn != nil {
		depends := map[string]interface{}{}

		for _, d := range s.DependsOn {
			if d.Condition == "" {
				depends[d.Name] = struct{}{}
			} else {
				depends[d.Name] = map[string]interface{}{
					"condition": d.Condition,
				}
			}
		}
		service["depends_on"] = depends
	}

	if runtime.GOOS == "linux" {
		// We rely on host.docker.internal as the DNS address for the host inside
		// the container. But, this is only available on Macos and Windows.
		// On Linux, you can use the IP address 172.17.0.1 to access the host.
		// Thus, if we are running on Linux, we need to add an extra host entry.
		service["extra_hosts"] = map[string]string{
			"host.docker.internal": "172.17.0.1",
		}
	}

	if s.Entrypoint != "" {
		service["entrypoint"] = s.Entrypoint
	}

	if len(s.Ports) > 0 {
		ports := []string{}
		for _, p := range s.Ports {
			protocol := ""
			if p.Protocol == ProtocolUDP {
				protocol = "/udp"
			}

			// CADDY Modification
			// TODO(Odysseas): This feels like a hack here
			// Only expose ports if Caddy is not enabled or this is the Caddy service itself
			shouldExpose := !d.manifest.ctx.CaddyEnabled || s.Name == "caddy"

			if shouldExpose {
				if d.bindHostPortsLocally {
					ports = append(ports, fmt.Sprintf("127.0.0.1:%d:%d%s", p.HostPort, p.Port, protocol))
				} else {
					ports = append(ports, fmt.Sprintf("%d:%d%s", p.HostPort, p.Port, protocol))
				}
			}
		}
		if len(ports) > 0 {
			service["ports"] = ports
		}
	}

	return service, nil
}

func (d *LocalRunner) isHostService(name string) bool {
	_, ok := d.overrides[name]
	return ok
}

func (d *LocalRunner) generateDockerCompose() ([]byte, error) {
	compose := map[string]interface{}{
		// We create a new network to be used by all the services so that
		// we can do DNS discovery between them.
		"networks": map[string]interface{}{
			d.networkName: map[string]interface{}{
				"name": d.networkName,
			},
		},
	}

	services := map[string]interface{}{}

	// for each service, reserve a port on the host machine. We use this ports
	// both to have access to the services from localhost but also to do communication
	// between services running inside docker and the ones running on the host machine.
	for _, svc := range d.manifest.Services {
		// CADDY Modification
		// TODO(Odysseas): This feels like a hack here
		// If caddy is enabled, then reserve ports only for caddy
		// else, reserve for all services
		if d.manifest.ctx.CaddyEnabled {
			if svc.Name == "caddy" {
				// caddy is a special case. We need to reserve the ports for http, https and admin
				// and then we need to add the routes to the Caddyfile
				for _, port := range svc.Ports {
					port.HostPort = d.reservePort(port.Port, port.Protocol)
				}
			}
		} else {
			for _, port := range svc.Ports {
				port.HostPort = d.reservePort(port.Port, port.Protocol)
			}
		}
	}
	for _, svc := range d.manifest.Services {
		if d.isHostService(svc.Name) {
			// skip services that are going to be launched on host
			continue
		}
		var err error
		if services[svc.Name], err = d.toDockerComposeService(svc); err != nil {
			return nil, fmt.Errorf("failed to convert service %s to docker compose service: %w", svc.Name, err)
		}
	}

	compose["services"] = services
	yamlData, err := yaml.Marshal(compose)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker compose: %w", err)
	}

	return yamlData, nil
}

func (d *LocalRunner) createVolume(service, volumeName string) (string, error) {
	// create the volume in the output folder
	volumeDirAbsPath, err := d.out.CreateDir(fmt.Sprintf("volume-%s-%s", service, volumeName))
	if err != nil {
		return "", fmt.Errorf("failed to create volume dir %s: %w", volumeName, err)
	}
	return volumeDirAbsPath, nil
}

// runOnHost runs the service on the host machine
func (d *LocalRunner) runOnHost(ss *Service) error {
	// TODO: Use env vars in host processes
	args, _, err := d.applyTemplate(ss)
	if err != nil {
		return fmt.Errorf("failed to apply template, err: %w", err)
	}

	// Create the volumes for this service
	volumesMapped := map[string]string{}
	for pathInDocker, volumeName := range ss.VolumesMapped {
		volumeDirAbsPath, err := d.createVolume(ss.Name, volumeName)
		if err != nil {
			return err
		}
		volumesMapped[pathInDocker] = volumeDirAbsPath
	}

	// We have to replace the names of the files it is using as artifacts for the full names
	// Just a string replacement should be enough
	for i, arg := range args {
		// If any of the args contains any of the files mapped, we need to replace it
		for pathInDocker, artifactName := range ss.FilesMapped {
			if strings.Contains(arg, pathInDocker) {
				args[i] = strings.ReplaceAll(arg, pathInDocker, filepath.Join(d.out.dst, artifactName))
			}
		}
		// If any of the args contains any of the volumes mapped, we need to create
		// the volume and replace it
		for pathInDocker, volumeAbsPath := range volumesMapped {
			if strings.Contains(arg, pathInDocker) {
				args[i] = strings.ReplaceAll(arg, pathInDocker, volumeAbsPath)
			}
		}
	}

	execPath := d.overrides[ss.Name]
	cmd := exec.Command(execPath, args...)

	logOutput, err := d.out.LogOutput(ss.Name)
	if err != nil {
		// this should not happen, log it
		logOutput = os.Stdout
	}

	// Output the command itself to the log output for debugging purposes
	fmt.Fprint(logOutput, strings.Join(args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	go func() {
		if err := cmd.Run(); err != nil {
			d.exitErr <- fmt.Errorf("error running host service %s: %w", ss.Name, err)
		}
	}()

	// we do not need to lock this array because we run the host services sequentially
	d.handles = append(d.handles, cmd)
	return nil
}

// trackLogs tracks the logs of a container and writes them to the log output
func (d *LocalRunner) trackLogs(serviceName string, containerID string) error {
	d.tasksMtx.Lock()
	log_output := d.tasks[serviceName].logs
	d.tasksMtx.Unlock()

	if log_output == nil {
		panic("BUG: log output not found for service " + serviceName)
	}

	logs, err := d.client.ContainerLogs(context.Background(), containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return fmt.Errorf("error getting container logs: %w", err)
	}

	if _, err := stdcopy.StdCopy(log_output, log_output, logs); err != nil {
		return fmt.Errorf("error copying logs: %w", err)
	}

	return nil
}

func (d *LocalRunner) trackContainerStatusAndLogs() {
	eventCh, errCh := d.client.Events(context.Background(), events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", fmt.Sprintf("playground.session=%s", d.sessionID))),
	})

	for {
		select {
		case event := <-eventCh:
			name := event.Actor.Attributes["com.docker.compose.service"]

			switch event.Action {
			case events.ActionStart:
				d.updateTaskStatus(name, taskStatusStarted)

				if d.logInternally {
					// the container has started, we can track the logs now
					go func() {
						if err := d.trackLogs(name, event.Actor.ID); err != nil {
							log.Warn("error tracking logs", "error", err)
						}
					}()
				}
			case events.ActionDie:
				d.updateTaskStatus(name, taskStatusDie)
				log.Info("container died", "name", name)

			case events.ActionHealthStatusHealthy:
				d.updateTaskStatus(name, taskStatusHealthy)
				log.Info("container is healthy", "name", name)
			}

		case err := <-errCh:
			log.Warn("error tracking events", "error", err)
		}
	}
}

func CreatePrometheusServices(manifest *Manifest, out *output) error {
	// Read all the components to be deployed and find all the ports with name 'metrics'
	// to create the prometheus scrapper config
	var scrapeConfigs []map[string]interface{}

	// global scrape config
	scrapeConfigs = append(scrapeConfigs, map[string]interface{}{
		"job_name":     "external",
		"metrics_path": "/metrics",
		"static_configs": []map[string]interface{}{
			{
				"targets": []string{"host.docker.internal:5555"},
			},
		},
	})

	for _, c := range manifest.Services {
		for _, port := range c.Ports {
			if port.Name == "metrics" {
				metricsPath := "/metrics"
				if overrideMetricsPath, ok := c.Labels["metrics_path"]; ok {
					metricsPath = overrideMetricsPath
				}

				scrapeConfig := map[string]interface{}{
					"job_name":     c.Name,
					"metrics_path": metricsPath,
					"static_configs": []map[string]interface{}{
						{
							"targets": []string{fmt.Sprintf("%s:%d", c.Name, port.Port)},
						},
					},
				}
				scrapeConfigs = append(scrapeConfigs, scrapeConfig)
			}
		}
	}

	promConfig := map[string]interface{}{
		"global": map[string]interface{}{
			"scrape_interval":     "1s",
			"evaluation_interval": "1s",
		},
		"scrape_configs": scrapeConfigs,
	}

	if err := out.WriteFile("prometheus.yaml", promConfig); err != nil {
		return fmt.Errorf("failed to write prometheus.yml: %w", err)
	}

	// add to the manifest the prometheus service
	// This is a bit of a hack.
	srv := manifest.NewService("prometheus").
		WithImage("prom/prometheus").
		WithTag("latest").
		WithArgs("--config.file", "/data/prometheus.yaml").
		WithPort("metrics", 9090, "tcp").
		WithArtifact("/data/prometheus.yaml", "prometheus.yaml")
	srv.ComponentName = "null" // For now, later on we can create a Prometheus component
	manifest.Services = append(manifest.Services, srv)

	return nil
}

func (d *LocalRunner) Run() error {
	go d.trackContainerStatusAndLogs()

	yamlData, err := d.generateDockerCompose()
	if err != nil {
		return fmt.Errorf("failed to generate docker-compose.yaml: %w", err)
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return fmt.Errorf("failed to write docker-compose.yaml: %w", err)
	}

	// generate the output log file for each service so that it is available after Run is done
	for _, instance := range d.instances {
		if instance.logs != nil {
			d.tasks[instance.service.Name].logs = instance.logs.logRef
		}
	}

	// First start the services that are running in docker-compose
	cmd := exec.Command("docker", "compose", "-f", d.out.dst+"/docker-compose.yaml", "up", "-d")

	var errOut bytes.Buffer
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run docker-compose: %w, err: %s", err, errOut.String())
	}

	// Second, start the services that are running on the host machine
	errCh := make(chan error)
	go func() {
		for _, svc := range d.manifest.Services {
			if d.isHostService(svc.Name) {
				if err := d.runOnHost(svc); err != nil {
					errCh <- err
				}
			}
		}
		close(errCh)
	}()

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}
