package playground

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/ethereum/go-ethereum/log"
	"github.com/flashbots/builder-playground/utils"
	"github.com/flashbots/builder-playground/utils/mainctx"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v2"
)

const (
	defaultNetworkName  = "ethplayground"
	stopGracePeriodSecs = 30
)

// LocalRunner is a component that runs the services from the manifest on the local host machine.
// By default, it uses docker and docker compose to run all the services.
// But, some services (if they are configured to do so) can be run on the host machine instead.
// When running inside docker, each service will use the port numbers they define in the component description.
// Besides, they will also bind to an available public port on the host machine.
// If the service runs on the host, it will use the host port numbers instead directly.
type LocalRunner struct {
	config *RunnerConfig

	out      *output
	manifest *Manifest
	client   *client.Client

	// reservedPorts is a map of port numbers reserved for each service to avoid conflicts
	// since we reserve ports for all the services before they are used
	reservedPorts map[int]bool

	// handles stores the references to the processes that are running on host machine
	// they are executed sequentially so we do not need to lock the handles
	handles []*exec.Cmd

	// exitError signals when one of the services fails
	exitErr     chan error
	exitErrOnce sync.Once

	// tasks tracks the status of each service
	tasksMtx        sync.Mutex
	tasks           map[string]*task
	allTasksReadyCh chan struct{}
}

type task struct {
	status TaskStatus
	ready  bool
	logs   *os.File
}

type TaskStatus string

var (
	TaskStatusPulling  TaskStatus = "pulling"
	TaskStatusPulled   TaskStatus = "pulled"
	TaskStatusPending  TaskStatus = "pending"
	TaskStatusStarted  TaskStatus = "started"
	TaskStatusDie      TaskStatus = "die"
	TaskStatusHealthy  TaskStatus = "healthy"
	TaskStatusUnhealty TaskStatus = "unhealthy"
)

func newDockerClient() (*client.Client, error) {
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return client, nil
}

type RunnerConfig struct {
	Out                  *output
	Manifest             *Manifest
	BindHostPortsLocally bool
	NetworkName          string
	Labels               map[string]string
	LogInternally        bool
	Platform             string
	Callbacks            []Callback
}

func (r *RunnerConfig) AddCallback(c Callback) {
	if r.Callbacks == nil {
		r.Callbacks = append(r.Callbacks, c)
	}
}

type Callback func(serviceName string, update TaskStatus)

func NewLocalRunner(cfg *RunnerConfig) (*LocalRunner, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	// download any local release artifacts for the services that require them
	// TODO: it feels a bit weird to have all this logic on the new command. We should split it later on.
	for _, service := range cfg.Manifest.Services {
		if service.Labels[useHostExecutionLabel] == "true" {
			// If HostPath is already set, use it directly (user-provided binary path)
			if service.HostPath != "" {
				continue
			}
			// Otherwise, download the release artifact
			releaseArtifact := service.release
			if releaseArtifact == nil {
				return nil, fmt.Errorf("service '%s' requires either host_path or release configuration", service.Name)
			}
			bin, err := DownloadRelease(cfg.Out.homeDir, releaseArtifact)
			if err != nil {
				return nil, fmt.Errorf("failed to download release artifact for service '%s': %w", service.Name, err)
			}
			service.HostPath = bin
		}
	}

	tasks := map[string]*task{}
	for _, service := range cfg.Manifest.Services {
		var logs *os.File

		if cfg.LogInternally {
			if logs, err = cfg.Out.LogOutput(service.Name); err != nil {
				return nil, fmt.Errorf("error getting log output: %w", err)
			}
		}

		tasks[service.Name] = &task{
			status: TaskStatusPending,
			logs:   logs,
		}
	}

	if cfg.NetworkName == "" {
		cfg.NetworkName = defaultNetworkName
	}

	if cfg.Callbacks == nil {
		cfg.Callbacks = []Callback{func(serviceName string, update TaskStatus) {}} // noop
	}

	d := &LocalRunner{
		config:          cfg,
		out:             cfg.Out,
		manifest:        cfg.Manifest,
		client:          client,
		reservedPorts:   map[int]bool{},
		handles:         []*exec.Cmd{},
		tasks:           tasks,
		allTasksReadyCh: make(chan struct{}),
		exitErr:         make(chan error, 2),
	}

	return d, nil
}

func (d *LocalRunner) checkAndUpdateReadiness() {
	for name, task := range d.tasks {
		// ensure the task is not a host service
		if d.isHostService(name) {
			continue
		}

		// first ensure the task has started
		if task.status != TaskStatusStarted {
			return
		}

		// then ensure it is ready if it has a ready function
		svc := d.getService(name)
		if svc.ReadyCheck != nil {
			if !task.ready {
				return
			}
		}
	}

	select {
	case <-d.allTasksReadyCh:
		// Channel is already closed, do nothing
	default:
		// Channel is not closed yet, close it
		close(d.allTasksReadyCh)
	}
}

func (d *LocalRunner) WaitForReady(ctx context.Context) error {
	defer utils.StartTimer("docker.wait-for-ready")()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-d.allTasksReadyCh:
		return nil

	case err := <-d.exitErr:
		return err
	}
}

func (d *LocalRunner) emitCallback(name string, status TaskStatus) {
	for _, callback := range d.config.Callbacks {
		callback(name, status)
	}
}

func (d *LocalRunner) updateTaskStatus(name string, status TaskStatus) {
	d.tasksMtx.Lock()
	defer d.tasksMtx.Unlock()
	if status == TaskStatusHealthy {
		d.tasks[name].ready = true
	} else if status == TaskStatusUnhealty {
		d.tasks[name].ready = false
	} else {
		d.tasks[name].status = status
	}

	if status == TaskStatusDie {
		d.sendExitError(fmt.Errorf("container %s failed", name))
	}

	d.emitCallback(name, status)
	d.checkAndUpdateReadiness()
}

func (d *LocalRunner) ExitErr() <-chan error {
	return d.exitErr
}

func (d *LocalRunner) sendExitError(err error) {
	d.exitErrOnce.Do(func() {
		d.exitErr <- err
		close(d.exitErr)
	})
}

func (d *LocalRunner) Stop(keepResources bool) error {
	forceKillCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Keep an eye on the force kill requests.
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			return
		case <-mainctx.GetForceKillCtx().Done():
			d.stopAllProcessesWithSignal(os.Kill)
			ForceKillSession(d.manifest.ID, keepResources)
		}
	}(forceKillCtx)
	// Kill all the processes ran by playground on the host.
	// Possible to make a more graceful exit with os.Interrupt here
	// but preferring a quick exit for now.
	d.stopAllProcessesWithSignal(os.Kill)
	return StopSession(d.manifest.ID, keepResources)
}

func (d *LocalRunner) stopAllProcessesWithSignal(signal os.Signal) {
	for _, handle := range d.handles {
		stopProcessWithSignal(handle, signal)
	}
}

// stopProcessWithSignal waits for the process to be set in the handle
// and avoids a panic, with a hard timeout.
func stopProcessWithSignal(handle *exec.Cmd, signal os.Signal) {
	ticker := time.NewTicker(time.Millisecond * 200)
	defer ticker.Stop()
	timeout := time.After(time.Second * 5)
	for {
		select {
		case <-ticker.C:
			if handle.Process != nil {
				handle.Process.Signal(signal)
				return
			}
		case <-timeout:
			return
		}
	}
}

func StopSession(id string, keepResources bool) error {
	// stop the docker-compose
	args := []string{"compose", "-p", id}
	if keepResources {
		args = append(args, "stop")
	} else {
		args = append(args, "down", "-v") // removes containers and volumes
	}
	cmd := exec.CommandContext(context.Background(), "docker", args...)
	// Isolate terminal signals from the child process and avoid weird force-kill cases
	// and leftovers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error taking docker-compose down: %w\n%s", err, outBuf.String())
	}

	return nil
}

// ForceKillSession stops all containers for a session with a short grace period (SIGTERM, wait, SIGKILL)
func ForceKillSession(id string, keepResources bool) {
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("docker ps -q --filter label=playground.session=%s | xargs -r docker stop -s SIGKILL", id))
	_ = cmd.Run()
}

func GetLocalSessions() ([]string, error) {
	var sessions []string
	client, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	containers, err := client.ContainerList(context.Background(), container.ListOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}
	for _, container := range containers {
		if container.Labels["playground"] == "true" {
			sessions = append(sessions, container.Labels["playground.session"])
		}
	}
	// Return sorted unique occurences
	slices.Sort(sessions)
	return slices.Compact(sessions), nil
}

func GetSessionServices(session string) ([]string, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	containers, err := client.ContainerList(context.Background(), container.ListOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}
	var serviceNames []string
	for _, container := range containers {
		if container.Labels["playground"] == "true" && container.Labels["playground.session"] == session {
			serviceNames = append(serviceNames, container.Labels["com.docker.compose.service"])
		}
	}
	return serviceNames, nil
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
		if d.config.BindHostPortsLocally {
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

	resolveAddr := func(targetSvc *Service, port *Port, protocol, user string) string {
		// - Service runs on host:
		//   A: target is inside docker: access with localhost:hostPort
		//   B: target is on the host: access with localhost:hostPort
		// - Service runs inside docker:
		//   C: target is inside docker: access it with DNS service:port
		//   D: target is on the host: access it with host.docker.internal:hostPort
		if d.isHostService(s.Name) {
			// A and B
			return printAddr(protocol, "localhost", port.HostPort, user)
		} else {
			if d.isHostService(targetSvc.Name) {
				// D
				return printAddr(protocol, "host.docker.internal", port.HostPort, user)
			}
			// C
			return printAddr(protocol, targetSvc.Name, port.Port, user)
		}
	}

	funcs := template.FuncMap{
		"Service": func(name, portLabel, protocol, user string) string {
			svc := d.manifest.MustGetService(name)
			port := svc.MustGetPort(portLabel)
			return resolveAddr(svc, port, protocol, user)
		},
		"Port": func(name string, defaultPort int) int {
			return resolvePort(name, defaultPort, ProtocolTCP)
		},
		"PortUDP": func(name string, defaultPort int) int {
			return resolvePort(name, defaultPort, ProtocolUDP)
		},
		"Bootnode": func() string {
			if d.manifest.Bootnode == nil {
				return ""
			}
			svc := d.manifest.MustGetService(d.manifest.Bootnode.Service)
			port := svc.MustGetPort("rpc")
			return resolveAddr(svc, port, "enode", d.manifest.Bootnode.ID)
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
		return fmt.Sprintf("%s%s@%s:%d", protocolPrefix, user, serviceName, port)
	}

	return fmt.Sprintf("%s%s:%d", protocolPrefix, serviceName, port)
}

func (d *LocalRunner) validateImageExists(image string) error {
	// check locally
	_, err := d.client.ImageInspect(context.Background(), image)
	if err == nil {
		return nil
	}
	if !client.IsErrNotFound(err) {
		return err
	}

	// check remotely
	if _, err = d.client.DistributionInspect(context.Background(), image, ""); err == nil {
		return nil
	}
	if !client.IsErrNotFound(err) {
		return err
	}

	return fmt.Errorf("image %s not found", image)
}

func (d *LocalRunner) toDockerComposeService(s *Service) (map[string]interface{}, []string, error) {
	// apply the template again on the arguments to figure out the connections
	// at this point all of them are valid, we just have to resolve them again. We assume for now
	// everyone is going to be on docker at the same network.
	args, envs, err := d.applyTemplate(s)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to apply template, err: %w", err)
	}

	// The containers have access to the full set of artifacts on the /artifacts folder
	// so, we have to bind it as a volume on the container.
	outputFolder, err := d.out.AbsoluteDstPath()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get absolute path for output folder: %w", err)
	}

	// Validate that the image exists
	imageName := fmt.Sprintf("%s:%s", s.Image, s.Tag)
	if err := d.validateImageExists(imageName); err != nil {
		return nil, nil, fmt.Errorf("failed to validate image %s: %w", imageName, err)
	}

	labels := map[string]string{
		// It is important to use the playground label to identify the containers
		// during the cleanup process
		"playground":         "true",
		"playground.session": d.manifest.ID,
		"service":            s.Name,
	}

	// apply the user defined labels
	maps.Copy(labels, d.config.Labels)

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
	var createdVolumes []string
	for localPath, volume := range s.VolumesMapped {
		dockerVolumeName := d.createVolumeName(s.Name, volume.Name)

		if volume.IsLocal {
			absPath := utils.MustGetVolumeDir(d.manifest.ID, dockerVolumeName)
			volumes[absPath] = localPath
		} else {
			volumes[dockerVolumeName] = localPath
			createdVolumes = append(createdVolumes, dockerVolumeName)
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
		"networks":          []string{d.config.NetworkName},
		"labels":            labels,
		"stop_grace_period": fmt.Sprintf("%ds", stopGracePeriodSecs),
	}

	if d.config.Platform != "" {
		service["platform"] = d.config.Platform
	}

	if len(envs) > 0 {
		service["environment"] = envs
	}

	if s.ReadyCheck != nil {
		if s.ReadyCheck.Test == nil {
			return nil, nil, fmt.Errorf("ready check for service %s must define either Test or QueryURL", s.Name)
		}

		service["healthcheck"] = map[string]interface{}{
			"test":         s.ReadyCheck.Test,
			"interval":     s.ReadyCheck.Interval.String(),
			"timeout":      s.ReadyCheck.Timeout.String(),
			"retries":      s.ReadyCheck.Retries,
			"start_period": s.ReadyCheck.StartPeriod.String(),
		}
	}

	if s.UngracefulShutdown {
		service["stop_grace_period"] = "0s"
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

			if d.config.BindHostPortsLocally {
				ports = append(ports, fmt.Sprintf("127.0.0.1:%d:%d%s", p.HostPort, p.Port, protocol))
			} else {
				ports = append(ports, fmt.Sprintf("%d:%d%s", p.HostPort, p.Port, protocol))
			}
		}
		service["ports"] = ports
	}

	return service, createdVolumes, nil
}

func (d *LocalRunner) isHostService(name string) bool {
	return d.manifest.MustGetService(name).HostPath != ""
}

func (d *LocalRunner) generateDockerCompose() ([]byte, error) {
	compose := map[string]interface{}{
		// We create a new network to be used by all the services so that
		// we can do DNS discovery between them.
		"networks": map[string]interface{}{
			d.config.NetworkName: map[string]interface{}{
				"name": d.config.NetworkName,
			},
		},
	}

	services := map[string]interface{}{}

	// for each service, reserve a port on the host machine. We use this ports
	// both to have access to the services from localhost but also to do communication
	// between services running inside docker and the ones running on the host machine.
	for _, svc := range d.manifest.Services {
		for _, port := range svc.Ports {
			port.HostPort = d.reservePort(port.Port, port.Protocol)
		}
	}

	volumes := map[string]struct{}{}
	for _, svc := range d.manifest.Services {
		if d.isHostService(svc.Name) {
			// skip services that are going to be launched on host
			continue
		}
		var (
			err           error
			dockerVolumes []string
		)
		services[svc.Name], dockerVolumes, err = d.toDockerComposeService(svc)
		if err != nil {
			return nil, fmt.Errorf("failed to convert service %s to docker compose service: %w", svc.Name, err)
		}
		for _, volumeName := range dockerVolumes {
			volumes[volumeName] = struct{}{}
		}
	}

	compose["services"] = services
	compose["volumes"] = volumes
	yamlData, err := yaml.Marshal(compose)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker compose: %w", err)
	}

	return yamlData, nil
}

func (d *LocalRunner) createVolumeName(service, volumeName string) string {
	// Check if this is a shared volume (prefixed with "shared:")
	if strings.HasPrefix(volumeName, "shared:") {
		// Shared volumes don't include service name prefix
		return fmt.Sprintf("volume-%s", strings.TrimPrefix(volumeName, "shared:"))
	}
	return fmt.Sprintf("volume-%s-%s", service, volumeName)
}

func (d *LocalRunner) createVolumeDir(service, volumeName string) (string, error) {
	// create the volume in the output folder
	var dirName string
	if strings.HasPrefix(volumeName, "shared:") {
		dirName = fmt.Sprintf("volume-%s", strings.TrimPrefix(volumeName, "shared:"))
	} else {
		dirName = fmt.Sprintf("volume-%s-%s", service, volumeName)
	}
	volumeDirAbsPath, err := d.out.CreateDir(dirName)
	if err != nil {
		return "", fmt.Errorf("failed to create volume dir %s: %w", volumeName, err)
	}
	return volumeDirAbsPath, nil
}

// waitForDependencies waits for all dependencies of a host service to be healthy
func (d *LocalRunner) waitForDependencies(ss *Service) error {
	if len(ss.DependsOn) == 0 {
		return nil
	}

	for _, dep := range ss.DependsOn {
		if dep.Condition != DependsOnConditionHealthy {
			continue
		}

		// The dependency name might be a healthmon sidecar (e.g., "el_healthmon")
		// We need to check the original service name
		depName := dep.Name
		originalName := strings.TrimSuffix(depName, "_healthmon")

		// Check if the original service is a host service
		if d.isHostService(originalName) {
			depSvc := d.manifest.MustGetService(originalName)

			// Determine the URL to check for health
			var checkURL string
			if depSvc.ReadyCheck != nil && depSvc.ReadyCheck.QueryURL != "" {
				checkURL = depSvc.ReadyCheck.QueryURL
			} else if httpPort, ok := depSvc.GetPort("http"); ok {
				checkURL = fmt.Sprintf("http://localhost:%d", httpPort.HostPort)
			} else {
				return fmt.Errorf("host service %s has no ready_check or http port defined", originalName)
			}

			// Poll until the dependency is healthy
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			slog.Info("Waiting for host dependency", "service", ss.Name, "dependency", originalName)

			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("timeout waiting for %s to be healthy", originalName)
				default:
				}

				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Get(checkURL)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode < 500 {
						slog.Info("Dependency is healthy", "service", ss.Name, "dependency", originalName)
						break
					}
				}

				time.Sleep(1 * time.Second)
			}
		} else {
			// For Docker services, check the healthmon container
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			slog.Info("Waiting for container dependency", "service", ss.Name, "dependency", depName)

			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("timeout waiting for %s to be healthy", depName)
				default:
				}

				resp, err := ExecuteHealthCheckManually(depName)
				if err == nil && resp.ExitCode == 0 {
					slog.Info("Dependency is healthy", "service", ss.Name, "dependency", depName)
					break
				}

				time.Sleep(1 * time.Second)
			}
		}
	}

	return nil
}

// runOnHost runs the service on the host machine
func (d *LocalRunner) runOnHost(ss *Service) error {
	// Wait for dependencies to be healthy before starting
	if err := d.waitForDependencies(ss); err != nil {
		return fmt.Errorf("failed waiting for dependencies: %w", err)
	}

	// TODO: Use env vars in host processes
	args, _, err := d.applyTemplate(ss)
	if err != nil {
		return fmt.Errorf("failed to apply template, err: %w", err)
	}

	// Create the volumes for this service
	volumesMapped := map[string]string{}
	for pathInDocker, volume := range ss.VolumesMapped {
		volumeDirAbsPath, err := d.createVolumeDir(ss.Name, volume.Name)
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

	execPath := ss.HostPath
	cmd := exec.Command(execPath, args...)
	cmd.Dir = d.out.dst // Run from artifacts directory so relative paths work

	logOutput, err := d.out.LogOutput(ss.Name)
	if err != nil {
		// this should not happen, log it
		logOutput = os.Stdout
	}
	logPath := logOutput.Name()

	// Output the command itself to the log output for debugging purposes
	cmdLine := execPath + " " + strings.Join(args, " ")
	fmt.Fprint(logOutput, cmdLine+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	go func() {
		if err := cmd.Run(); err != nil {
			// If the playground is being exited, ignore the exit error info
			// to make the outputs less confusing.
			if mainctx.IsExiting() {
				return
			}
			// Read last lines from log file for context
			lastLines := readLastLines(logPath, 10)
			slog.Error("Host service failed", "service", ss.Name, "error", err)
			errMsg := fmt.Sprintf("service %s failed:\n  Command: %s\n  Log file: %s\n  Exit error: %v",
				ss.Name, execPath, logPath, err)
			if lastLines != "" {
				errMsg += fmt.Sprintf("\n  Last output:\n%s", lastLines)
			}
			d.sendExitError(fmt.Errorf("%s", errMsg))
		}
	}()

	// we do not need to lock this array because we run the host services sequentially
	d.handles = append(d.handles, cmd)
	return nil
}

// trackLogs tracks the logs of a container and writes them to the log output
func (d *LocalRunner) trackLogs(serviceName, containerID string) error {
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
		Filters: filters.NewArgs(filters.Arg("label", fmt.Sprintf("playground.session=%s", d.manifest.ID))),
	})

	for {
		select {
		case event := <-eventCh:
			name := event.Actor.Attributes["com.docker.compose.service"]

			switch event.Action {
			case events.ActionStart:
				d.updateTaskStatus(name, TaskStatusStarted)

				if d.config.LogInternally {
					// the container has started, we can track the logs now
					go func() {
						if err := d.trackLogs(name, event.Actor.ID); err != nil {
							log.Warn("error tracking logs", "error", err)
						}
					}()
				}
			case events.ActionDie:
				d.updateTaskStatus(name, TaskStatusDie)
				log.Info("container died", "name", name)

			case events.ActionHealthStatusHealthy:
				d.updateTaskStatus(name, TaskStatusHealthy)
				log.Info("container is healthy", "name", name)

			case events.ActionHealthStatusUnhealthy:
				d.updateTaskStatus(name, TaskStatusUnhealty)
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
	manifest.Services = append(manifest.Services, srv)

	return nil
}

func (d *LocalRunner) ensureImage(ctx context.Context, imageName string) error {
	// Check if image exists locally
	_, err := d.client.ImageInspect(ctx, imageName)
	if err == nil {
		return nil // Image already exists
	}
	if !client.IsErrNotFound(err) {
		return err
	}

	// Image not found locally, pull it
	d.emitCallback(imageName, TaskStatusPulling)

	slog.Info("pulling image", "image", imageName)
	reader, err := d.client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Consume the output to ensure pull completes
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("failed to read image pull output %s: %w", imageName, err)
	}

	d.emitCallback(imageName, TaskStatusPulled)
	return nil
}

func (d *LocalRunner) pullNotAvailableImages(ctx context.Context) error {
	defer utils.StartTimer("docker.pull-available-images")()

	g, ctx := errgroup.WithContext(ctx)

	for _, svc := range d.manifest.Services {
		if d.isHostService(svc.Name) {
			continue // Skip host services
		}

		s := svc // Capture loop variable
		g.Go(func() error {
			imageName := fmt.Sprintf("%s:%s", s.Image, s.Tag)
			if err := d.ensureImage(ctx, imageName); err != nil {
				return fmt.Errorf("failed to ensure image %s: %w", imageName, err)
			}
			return nil
		})
	}

	return g.Wait()
}

func (d *LocalRunner) Run(ctx context.Context) error {
	go d.trackContainerStatusAndLogs()

	yamlData, err := d.generateDockerCompose()
	if err != nil {
		return fmt.Errorf("failed to generate docker-compose.yaml: %w", err)
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return fmt.Errorf("failed to write docker-compose.yaml: %w", err)
	}

	// Pull all required images in parallel
	if err := d.pullNotAvailableImages(ctx); err != nil {
		return err
	}

	defer utils.StartTimer("docker.up")()

	// First start the services that are running in docker-compose
	cmd := exec.CommandContext(
		ctx, "docker", "compose",
		"-p", d.manifest.ID, // identify project with id for doing "docker compose down" on it later
		"-f", d.out.dst+"/docker-compose.yaml",
		"up",
		"-d",
	)

	var errOut bytes.Buffer
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		// Don't return error if context was cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to run docker-compose: %w, err: %s", err, errOut.String())
	}

	// Second, start the services that are running on the host machine
	// Start them in parallel - each will wait for its own dependencies
	g := new(errgroup.Group)
	for _, svc := range d.manifest.Services {
		if d.isHostService(svc.Name) {
			svc := svc // capture loop variable
			g.Go(func() error {
				return d.runOnHost(svc)
			})
		}
	}

	return g.Wait()
}

type HealthCheckResponse struct {
	Output   string
	ExitCode int
}

func ExecuteHealthCheckManually(serviceName string) (*HealthCheckResponse, error) {
	ctx := context.Background()

	cli, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	containerID, err := findServiceByName(cli, ctx, serviceName)
	if err != nil {
		return nil, err
	}

	// Get the container to find the health check command
	containerJSON, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	if containerJSON.Config.Healthcheck == nil {
		return nil, fmt.Errorf("container has no health check configured")
	}

	healthCheckCmd := containerJSON.Config.Healthcheck.Test

	// Health check commands are usually in format: ["CMD-SHELL", "actual command"]
	// or ["CMD", "arg1", "arg2", ...]
	var execCmd []string

	if len(healthCheckCmd) == 0 {
		return nil, fmt.Errorf("health check command is empty")
	}

	if healthCheckCmd[0] == "CMD-SHELL" {
		// Use sh -c to execute the shell command
		if len(healthCheckCmd) > 1 {
			execCmd = []string{"sh", "-c", healthCheckCmd[1]}
		} else {
			return nil, fmt.Errorf("CMD-SHELL specified but no command provided")
		}
	} else if healthCheckCmd[0] == "CMD" {
		// Direct command execution
		execCmd = healthCheckCmd[1:]
	} else {
		// Assume it's a direct command
		execCmd = healthCheckCmd
	}

	// Create exec instance
	execConfig := container.ExecOptions{
		Cmd:          execCmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec: %w", err)
	}

	// Start the exec and get output
	resp, err := cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer resp.Close()

	// Read all output
	var outBuf bytes.Buffer
	_, err = io.Copy(&outBuf, resp.Reader)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("error reading output: %w", err)
	}

	// Get exit code
	inspectResp, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect exec: %w", err)
	}

	healthCheckResp := &HealthCheckResponse{
		Output:   strings.TrimSpace(outBuf.String()),
		ExitCode: inspectResp.ExitCode,
	}
	return healthCheckResp, nil
}

func findServiceByName(client *client.Client, ctx context.Context, serviceName string) (string, error) {
	containers, err := client.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return "", fmt.Errorf("error getting container list: %w", err)
	}

	for _, container := range containers {
		if container.Labels["playground"] == "true" &&
			container.Labels["com.docker.compose.service"] == serviceName {
			return container.ID, nil
		}
	}

	return "", nil
}

// readLastLines reads the last n lines from a file
func readLastLines(filePath string, n int) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) <= n {
		return "    " + strings.Join(lines, "\n    ")
	}

	lastLines := lines[len(lines)-n-1:]
	return "    " + strings.Join(lastLines, "\n    ")
}
