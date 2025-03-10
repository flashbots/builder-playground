package internal

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"gopkg.in/yaml.v2"
)

type DockerRunner struct {
	out           *output
	svcManager    *serviceManager
	composeCmd    *exec.Cmd
	ctx           context.Context
	cancel        context.CancelFunc
	client        *client.Client
	reservedPorts map[int]bool
	overrides     map[string]string
	handles       []*exec.Cmd
}

func NewDockerRunner(out *output, svcManager *serviceManager, overrides map[string]string) *DockerRunner {
	ctx, cancel := context.WithCancel(context.Background())

	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	return &DockerRunner{
		out:           out,
		svcManager:    svcManager,
		ctx:           ctx,
		cancel:        cancel,
		client:        client,
		reservedPorts: map[int]bool{},
		overrides:     overrides,
		handles:       []*exec.Cmd{},
	}
}

func (d *DockerRunner) Stop() {
	fmt.Println("Stopping all containers")

	// try to stop all the containers from the container list for playground
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
	})
	if err != nil {
		fmt.Println("Error getting container list:", err)
		return
	}

	fmt.Printf("Found %d containers to stop\n", len(containers))

	var wg sync.WaitGroup
	wg.Add(len(containers))

	for _, cont := range containers {
		fmt.Println("Stopping container:", cont.ID)

		go func(contID string) {
			defer wg.Done()
			if err := d.client.ContainerRemove(context.Background(), contID, container.RemoveOptions{
				RemoveVolumes: true,
				RemoveLinks:   false,
				Force:         true,
			}); err != nil {
				fmt.Println("Error removing container:", err)
			}
		}(cont.ID)
	}

	wg.Wait()

	// stop all the handles
	for _, handle := range d.handles {
		handle.Process.Kill()
	}
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
		if svc.name == name {
			return svc
		}
	}
	return nil
}

func (d *DockerRunner) applyTemplate(s *service) []string {
	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
			// find the service and the port that it resolves for that label
			svc := d.getService(name)
			if svc == nil {
				panic(fmt.Sprintf("BUG: service %s not found", name))
			}
			port := svc.GetPort(portLabel)
			if port == nil {
				panic(fmt.Sprintf("BUG: port label %s not found for service %s", portLabel, name))
			}

			if s.override == "" {
				// service is running inside docker
				if svc.override == "" {
					// use the DNS discovery of docker compose to connect to the service and the docker port
					return fmt.Sprintf("http://%s:%d", svc.name, port.port)
				}
				// the service is going to be running with the host port in the host machine
				// use host.docker.internal to connect to it.
				return fmt.Sprintf("http://host.docker.internal:%d", port.hostPort)
			} else {
				// either if the target service is running inside docker or outside, it is exposed in localhost
				// with the host port
				return fmt.Sprintf("http://localhost:%d", port.hostPort)
			}
		},
		"Port": func(name string, defaultPort int) int {
			if s.override == "" {
				// running inside docker, return the port
				return defaultPort
			}
			// return the host port
			return s.GetPort(name).hostPort
		},
	}

	var argsResult []string
	for _, arg := range s.args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			panic(fmt.Sprintf("BUG: failed to parse template, err: %s, arg: %s", err, arg))
		}

		var out strings.Builder
		if err := tpl.Execute(&out, nil); err != nil {
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

	service := map[string]interface{}{
		"image":   fmt.Sprintf("%s:%s", s.imageReal, s.tag),
		"command": args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("./:/output"),
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
			ports = append(ports, fmt.Sprintf("%d:%d", p.hostPort, p.port))
		}
		service["ports"] = ports
	}

	return service
}

func (d *DockerRunner) GenerateDockerCompose() ([]byte, error) {
	// First, figure out if the overrides are valid, they might reference a service that does not exist.
	for name, val := range d.overrides {
		svc := d.getService(name)
		if svc == nil {
			return nil, fmt.Errorf("service %s from override not found", name)
		}
		svc.override = val
	}

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
			port.hostPort = d.reservePort(port.port)
		}
	}

	for _, svc := range d.svcManager.services {
		if svc.srvMng != nil { // Only include services that were created with NewService
			// resolve the template again for the variables because things Connect need to be resolved now.
			if svc.override != "" {
				// skip services that are going to be launched with an override
				continue
			}
			services[svc.name] = d.ToDockerComposeService(svc)
		}
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

	fmt.Println("Running", ss.override, args)
	cmd := exec.Command(ss.override, args...)

	logOutput, err := d.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	go func() {
		if err := cmd.Run(); err != nil {
			panic(err)
		}
	}()

	d.handles = append(d.handles, cmd)
}

func (d *DockerRunner) Run() error {
	yamlData, err := d.GenerateDockerCompose()
	if err != nil {
		return err
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return err
	}

	d.composeCmd = exec.Command("docker-compose", "-f", "./output/docker-compose.yaml", "up", "-d")

	// in parallel start the services that need to be overriten and ran on host
	go func() {
		for _, svc := range d.svcManager.services {
			if svc.override != "" {
				d.runOnHost(svc)
			}
		}
	}()

	go func() {
		fmt.Println("Starting event listener")

		// Ok, track all the events that happen for the playground=true contianers.
		eventCh, errCh := d.client.Events(context.Background(), events.ListOptions{
			Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
		})

		for {
			select {
			case event := <-eventCh:
				fmt.Println("--- event ---")
				name := event.Actor.Attributes["com.docker.compose.service"]
				fmt.Println(event.Action, event.Actor.ID, name)

				if event.Action == "start" {
					// track the container logs
					go func() {
						fmt.Println("Starting log listener for", name)

						log_output, err := d.out.LogOutput(name)
						if err != nil {
							fmt.Println("Error getting log output:", err)
							return
						}

						logs, err := d.client.ContainerLogs(context.Background(), event.Actor.ID, container.LogsOptions{
							ShowStdout: true,
							ShowStderr: true,
							Follow:     true,
						})
						if err != nil {
							fmt.Println("Error getting container logs:", err)
							return
						}

						if _, err := stdcopy.StdCopy(log_output, log_output, logs); err != nil {
							fmt.Println("Error copying logs:", err)
							return
						}
						fmt.Println("DONE")
					}()
				}
			case err := <-errCh:
				fmt.Println("--- err ---")
				fmt.Println(err)
			case <-d.ctx.Done():
				return
			}
		}
	}()

	return d.composeCmd.Run()
}
