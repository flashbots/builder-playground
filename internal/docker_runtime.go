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

type DockerRunner struct {
	out           *output
	svcManager    *Manifest
	composeCmd    *exec.Cmd
	ctx           context.Context
	cancel        context.CancelFunc
	client        *client.Client
	reservedPorts map[int]bool
	overrides     map[string]string
	// handles are the handles to the processes that are running on host machine
	handles []*exec.Cmd

	// exitError signals when one of the container fails
	exitErr chan error

	// signals whether we are running in interactive mode
	interactive bool

	// tasks are the tasks that are running in docker
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

func NewDockerRunner(out *output, svcManager *Manifest, overrides map[string]string, interactive bool) (*DockerRunner, error) {
	ctx, cancel := context.WithCancel(context.Background())

	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	tasks := map[string]string{}
	for _, svc := range svcManager.services {
		tasks[svc.Name] = taskStatusPending
	}

	d := &DockerRunner{
		out:           out,
		svcManager:    svcManager,
		ctx:           ctx,
		cancel:        cancel,
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

		// do an initial update to print the status
		select {
		case d.taskUpdateCh <- struct{}{}:
		default:
		}
	}

	return d, nil
}

func (d *DockerRunner) printStatus() {
	fmt.Print("\033[s")
	lineOffset := 0

	// Get ordered service names from manifest
	orderedServices := make([]string, 0, len(d.svcManager.services))
	for _, svc := range d.svcManager.services {
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
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *DockerRunner) updateTaskStatus(name string, status string) {
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

func (d *DockerRunner) ExitErr() <-chan error {
	return d.exitErr
}

func (d *DockerRunner) Stop() error {
	// try to stop all the containers from the container list for playground
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
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

func (d *DockerRunner) reservePort(startPort int) int {
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

func (d *DockerRunner) getService(name string) *service {
	for _, svc := range d.svcManager.services {
		if svc.Name == name {
			return svc
		}
	}
	return nil
}

func (d *DockerRunner) applyTemplate(s *service) []string {
	input := map[string]interface{}{
		"Dir": "/artifacts",
	}

	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
			// find the service and the port that it resolves for that label
			svc := d.getService(name)
			if svc == nil {
				panic(fmt.Sprintf("BUG: service %s not found", name))
			}
			port, ok := svc.GetPort(portLabel)
			if !ok {
				panic(fmt.Sprintf("BUG: port label %s not found for service %s", portLabel, name))
			}

			if d.isOverride(s.Name) {
				// either if the target service is running inside docker or outside, it is exposed in localhost
				// with the host port
				return fmt.Sprintf("http://localhost:%d", port.HostPort)
			} else {
				// service is running inside docker
				if d.isOverride(svc.Name) {
					// the service is going to be running with the host port in the host machine
					// use host.docker.internal to connect to it.
					return fmt.Sprintf("http://host.docker.internal:%d", port.HostPort)
				}
				// use the DNS discovery of docker compose to connect to the service and the docker port
				return fmt.Sprintf("http://%s:%d", svc.Name, port.Port)
			}
		},
		"Port": func(name string, defaultPort int) int {
			if !d.isOverride(s.Name) {
				// running inside docker, return the port
				return defaultPort
			}
			// return the host port
			port, ok := s.GetPort(name)
			if !ok {
				panic(fmt.Sprintf("BUG: port %s not found for service %s", name, s.Name))
			}
			return port.HostPort
		},
	}

	var argsResult []string
	for _, arg := range s.args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			panic(fmt.Sprintf("BUG: failed to parse template, err: %s, arg: %s", err, arg))
		}

		var out strings.Builder
		if err := tpl.Execute(&out, input); err != nil {
			panic(fmt.Sprintf("BUG: failed to execute template, err: %s, arg: %s", err, arg))
		}
		argsResult = append(argsResult, out.String())
	}

	return argsResult
}

func (d *DockerRunner) ToDockerComposeService(s *service) map[string]interface{} {
	// apply the template again on the arguments to figure out the connections
	// at this point all of them are valid, we just have to resolve them again. We assume for now
	// everyone is going to be on docker at the same network.
	args := d.applyTemplate(s)

	outputFolder, err := d.out.AbsoluteDstPath()
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to get absolute path for output folder: %s", err))
	}

	service := map[string]interface{}{
		"image":   fmt.Sprintf("%s:%s", s.image, s.tag),
		"command": args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("%s:/artifacts", outputFolder),
		},
		// Add the ethereum network
		"networks": []string{"ethereum"},
		"labels":   map[string]string{"playground": "true"},
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

	return service
}

func (d *DockerRunner) isOverride(name string) bool {
	_, ok := d.overrides[name]
	return ok
}

func (d *DockerRunner) GenerateDockerCompose() ([]byte, error) {
	compose := map[string]interface{}{
		"version":  "3.8",
		"services": map[string]interface{}{},
		// Add networks configuration
		"networks": map[string]interface{}{
			"ethereum": map[string]interface{}{
				"name": "ethereum",
			},
		},
	}

	services := compose["services"].(map[string]interface{})

	// for each of the ports, reserve a port on the host machine
	for _, svc := range d.svcManager.services {
		for _, port := range svc.ports {
			port.HostPort = d.reservePort(port.Port)
		}
	}

	for _, svc := range d.svcManager.services {
		// resolve the template again for the variables because things Connect need to be resolved now.
		if d.isOverride(svc.Name) {
			// skip services that are going to be launched with an override
			continue
		}
		services[svc.Name] = d.ToDockerComposeService(svc)
	}

	yamlData, err := yaml.Marshal(compose)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker-compose: %w", err)
	}

	return yamlData, nil
}

func (d *DockerRunner) runOnHost(ss *service) {
	// we have to apply the template to the args like we do in docker-compose services
	args := d.applyTemplate(ss)
	execPath := d.overrides[ss.Name]

	cmd := exec.Command(execPath, args...)

	logOutput, err := d.out.LogOutput(ss.Name)
	if err != nil {
		// this should not happen, log it
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	go func() {
		if err := cmd.Run(); err != nil {
			d.exitErr <- fmt.Errorf("error running host service %s: %w", ss.Name, err)
		}
	}()

	d.handles = append(d.handles, cmd)
}

func (d *DockerRunner) trackLogs(serviceName string, containerID string) error {
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

func (d *DockerRunner) trackContainerStatusAndLogs() {
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

		case <-d.ctx.Done():
			return
		}
	}
}

func (d *DockerRunner) Run() error {
	go d.trackContainerStatusAndLogs()

	yamlData, err := d.GenerateDockerCompose()
	if err != nil {
		return fmt.Errorf("failed to generate docker-compose: %w", err)
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return fmt.Errorf("failed to write docker-compose: %w", err)
	}

	d.composeCmd = exec.Command("docker-compose", "-f", d.out.dst+"/docker-compose.yaml", "up", "-d")

	var errOut bytes.Buffer
	d.composeCmd.Stderr = &errOut

	// on a separate goroutine start the services that need to be overriten and run on host
	go func() {
		for _, svc := range d.svcManager.services {
			if d.isOverride(svc.Name) {
				d.runOnHost(svc)
			}
		}
	}()

	if err := d.composeCmd.Run(); err != nil {
		return fmt.Errorf("failed to run docker-compose: %w, err: %s", err, errOut.String())
	}
	return nil
}
