package internal

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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
	"gopkg.in/yaml.v2"
)

const networkName = "ethplayground"

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

	// tasks tracks the status of each service
	tasksMtx     sync.Mutex
	tasks        map[string]*task
	taskUpdateCh chan struct{}

	// wether to bind the ports to the local interface
	bindHostPortsLocally bool
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

func NewLocalRunner(out *output, manifest *Manifest, overrides map[string]string, interactive bool, bindHostPortsLocally bool) (*LocalRunner, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	fmt.Println(bindHostPortsLocally)

	// merge the overrides with the manifest overrides
	if overrides == nil {
		overrides = make(map[string]string)
	}
	for k, v := range manifest.overrides {
		overrides[k] = v
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
			srv.image = parts[0]
			srv.tag = parts[1]

			delete(overrides, k)
			continue
		}
	}

	tasks := map[string]*task{}
	for _, svc := range manifest.services {
		tasks[svc.Name] = &task{
			status: taskStatusPending,
			logs:   nil,
		}
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

func (d *LocalRunner) printStatus() {
	fmt.Print("\033[s")
	lineOffset := 0

	// Get ordered service names from manifest
	orderedServices := make([]string, 0, len(d.manifest.services))
	for _, svc := range d.manifest.services {
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
		if svc.readyCheck != nil {
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
	containers, err := d.client.ContainerList(context.Background(), container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
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
func (d *LocalRunner) reservePort(startPort int) int {
	for i := startPort; i < startPort+1000; i++ {
		if _, ok := d.reservedPorts[i]; ok {
			continue
		}
		// make a net.Listen on the port to see if it is aavailable
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", i))
		if err != nil {
			continue
		}
		listener.Close()
		d.reservedPorts[i] = true
		return i
	}
	panic("BUG: could not reserve a port")
}

func (d *LocalRunner) getService(name string) *service {
	for _, svc := range d.manifest.services {
		if svc.Name == name {
			return svc
		}
	}
	return nil
}

// applyTemplate resolves the templates from the manifest (Dir, Port, Connect) into
// the actual values for this specific docker execution.
func (d *LocalRunner) applyTemplate(s *service) ([]string, map[string]string, error) {
	var input map[string]interface{}

	// For {{.Dir}}:
	// - Service runs on host: bind to the output folder
	// - Service runs inside docker: bind to /artifacts
	if d.isHostService(s.Name) {
		input = map[string]interface{}{
			"Dir": d.out.dst,
		}
	} else {
		input = map[string]interface{}{
			"Dir": "/artifacts",
		}
	}

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
		"Service": func(name string, portLabel, protocol string) string {
			protocolPrefix := ""
			if protocol == "http" {
				protocolPrefix = "http://"
			}

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
				return fmt.Sprintf("%slocalhost:%d", protocolPrefix, port.HostPort)
			} else {
				if d.isHostService(svc.Name) {
					// D
					return fmt.Sprintf("%shost.docker.internal:%d", protocolPrefix, port.HostPort)
				}
				// C
				return fmt.Sprintf("%s%s:%d", protocolPrefix, svc.Name, port.Port)
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
	for _, arg := range s.args {
		newArg, err := runTemplate(arg)
		if err != nil {
			return nil, nil, err
		}
		argsResult = append(argsResult, newArg)
	}

	// apply the templates to the environment variables
	envs := map[string]string{}
	for k, v := range s.env {
		newV, err := runTemplate(v)
		if err != nil {
			return nil, nil, err
		}
		envs[k] = newV
	}

	return argsResult, envs, nil
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

	return fmt.Errorf("image %s not found: %w", image)
}

func (d *LocalRunner) toDockerComposeService(s *service) (map[string]interface{}, error) {
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
	imageName := fmt.Sprintf("%s:%s", s.image, s.tag)
	if err := d.validateImageExists(imageName); err != nil {
		return nil, fmt.Errorf("failed to validate image %s: %w", imageName, err)
	}

	labels := map[string]string{
		// It is important to use the playground label to identify the containers
		// during the cleanup process
		"playground": "true",
		"service":    s.Name,
	}

	// add the local ports exposed by the service as labels
	// we have to do this for now since we do not store the manifest in JSON yet.
	// Otherwise, we could use that directly
	for _, port := range s.ports {
		labels[fmt.Sprintf("port.%s", port.Name)] = fmt.Sprintf("%d", port.Port)
	}

	// add the ports to the labels as well
	service := map[string]interface{}{
		"image":   imageName,
		"command": args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("%s:/artifacts", outputFolder),
		},
		// Add the ethereum network
		"networks": []string{networkName},
		"labels":   labels,
	}

	if len(envs) > 0 {
		service["environment"] = envs
	}

	if s.readyCheck != nil {
		var test []string
		if s.readyCheck.QueryURL != "" {
			// This is pretty much hardcoded for now.
			test = []string{"CMD-SHELL", "chmod +x /artifacts/scripts/query.sh && /artifacts/scripts/query.sh " + s.readyCheck.QueryURL}
		} else {
			test = s.readyCheck.Test
		}

		service["healthcheck"] = map[string]interface{}{
			"test":         test,
			"interval":     s.readyCheck.Interval.String(),
			"timeout":      s.readyCheck.Timeout.String(),
			"retries":      s.readyCheck.Retries,
			"start_period": s.readyCheck.StartPeriod.String(),
		}
	}

	if s.dependsOn != nil {
		depends := map[string]interface{}{}

		for _, d := range s.dependsOn {
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

	if s.entrypoint != "" {
		service["entrypoint"] = s.entrypoint
	}

	if len(s.ports) > 0 {
		ports := []string{}
		for _, p := range s.ports {
			protocol := ""
			if p.Protocol == ProtocolUDP {
				protocol = "/udp"
			}

			if d.bindHostPortsLocally {
				ports = append(ports, fmt.Sprintf("127.0.0.1:%d:%d%s", p.HostPort, p.Port, protocol))
			} else {
				ports = append(ports, fmt.Sprintf("%d:%d%s", p.HostPort, p.Port, protocol))
			}
		}
		service["ports"] = ports
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
			networkName: map[string]interface{}{
				"name": networkName,
			},
		},
	}

	services := map[string]interface{}{}

	// for each service, reserve a port on the host machine. We use this ports
	// both to have access to the services from localhost but also to do communication
	// between services running inside docker and the ones running on the host machine.
	for _, svc := range d.manifest.services {
		for _, port := range svc.ports {
			port.HostPort = d.reservePort(port.Port)
		}
	}

	for _, svc := range d.manifest.services {
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

// runOnHost runs the service on the host machine
func (d *LocalRunner) runOnHost(ss *service) error {
	// TODO: Use env vars in host processes
	args, _, err := d.applyTemplate(ss)
	if err != nil {
		return fmt.Errorf("failed to apply template, err: %w", err)
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
		Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
	})

	for {
		select {
		case event := <-eventCh:
			name := event.Actor.Attributes["com.docker.compose.service"]

			switch event.Action {
			case events.ActionStart:
				d.updateTaskStatus(name, taskStatusStarted)

				// the container has started, we can track the logs now
				go func() {
					if err := d.trackLogs(name, event.Actor.ID); err != nil {
						log.Warn("error tracking logs", "error", err)
					}
				}()
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

	for _, c := range manifest.services {
		for _, port := range c.ports {
			if port.Name == "metrics" {
				metricsPath := "/metrics"
				if overrideMetricsPath, ok := c.labels["metrics_path"]; ok {
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
		WithArgs("--config.file", "{{.Dir}}/prometheus.yaml").
		WithPort("metrics", 9090, "tcp")
	manifest.services = append(manifest.services, srv)

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
	for _, svc := range d.manifest.services {
		log_output, err := d.out.LogOutput(svc.Name)
		if err != nil {
			return fmt.Errorf("error getting log output: %w", err)
		}
		svc.logs = &serviceLogs{
			path: log_output.Name(),
		}
		d.tasks[svc.Name].logs = log_output
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
		for _, svc := range d.manifest.services {
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
