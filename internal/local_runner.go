package internal

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"

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
	tasks        map[string]string
	taskUpdateCh chan struct{}
}

var (
	taskStatusPending = "pending"
	taskStatusStarted = "started"
	taskStatusDie     = "die"
)

type taskUI struct {
	tasks    map[string]string
	spinners map[string]spinner.Model
	style    lipgloss.Style
}

func NewLocalRunner(out *output, manifest *Manifest, overrides map[string]string, interactive bool) (*LocalRunner, error) {
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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

	tasks := map[string]string{}
	for _, svc := range manifest.services {
		tasks[svc.Name] = taskStatusPending
	}

	d := &LocalRunner{
		out:           out,
		manifest:      manifest,
		client:        client,
		reservedPorts: map[int]bool{},
		overrides:     overrides,
		handles:       []*exec.Cmd{},
		tasks:         tasks,
		taskUpdateCh:  make(chan struct{}),
		exitErr:       make(chan error, 2),
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
				status := d.tasks[name]
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

func (d *LocalRunner) updateTaskStatus(name string, status string) {
	d.tasksMtx.Lock()
	defer d.tasksMtx.Unlock()
	d.tasks[name] = status

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
func (d *LocalRunner) applyTemplate(s *service) ([]string, error) {
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

	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
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
				return fmt.Sprintf("http://localhost:%d", port.HostPort)
			} else {
				if d.isHostService(svc.Name) {
					// D
					return fmt.Sprintf("http://host.docker.internal:%d", port.HostPort)
				}
				// C
				return fmt.Sprintf("http://%s:%d", svc.Name, port.Port)
			}
		},
		"Port": func(name string, defaultPort int) int {
			// For {{Port "name" "defaultPort"}}:
			// - Service runs on host: return the host port
			// - Service runs inside docker: return the docker port
			if d.isHostService(s.Name) {
				return defaultPort
			}
			return s.MustGetPort(name).HostPort
		},
	}

	var argsResult []string
	for _, arg := range s.args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			return nil, err
		}

		var out strings.Builder
		if err := tpl.Execute(&out, input); err != nil {
			return nil, err
		}
		argsResult = append(argsResult, out.String())
	}

	return argsResult, nil
}

func (d *LocalRunner) toDockerComposeService(s *service) (map[string]interface{}, error) {
	// apply the template again on the arguments to figure out the connections
	// at this point all of them are valid, we just have to resolve them again. We assume for now
	// everyone is going to be on docker at the same network.
	args, err := d.applyTemplate(s)
	if err != nil {
		return nil, fmt.Errorf("failed to apply template, err: %w", err)
	}

	// The containers have access to the full set of artifacts on the /artifacts folder
	// so, we have to bind it as a volume on the container.
	outputFolder, err := d.out.AbsoluteDstPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for output folder: %w", err)
	}

	service := map[string]interface{}{
		"image":   fmt.Sprintf("%s:%s", s.image, s.tag),
		"command": args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("%s:/artifacts", outputFolder),
		},
		// Add the ethereum network
		"networks": []string{networkName},
		// It is important to use the playground label to identify the containers
		// during the cleanup process
		"labels": map[string]string{"playground": "true"},
	}

	if s.entrypoint != "" {
		service["entrypoint"] = s.entrypoint
	}

	if len(s.ports) > 0 {
		ports := []string{}
		for _, p := range s.ports {
			ports = append(ports, fmt.Sprintf("%d:%d", p.HostPort, p.Port))
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
	args, err := d.applyTemplate(ss)
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
	log_output, err := d.out.LogOutput(serviceName)
	if err != nil {
		return fmt.Errorf("error getting log output: %w", err)
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
			}

		case err := <-errCh:
			log.Warn("error tracking events", "error", err)
		}
	}
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
