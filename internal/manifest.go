package internal

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/template"

	flag "github.com/spf13/pflag"
)

const useHostExecutionLabel = "use-host-execution"

type Recipe interface {
	Name() string
	Description() string
	Flags() *flag.FlagSet
	Artifacts() *ArtifactsBuilder
	Apply(ctx *ExContext, artifacts *Artifacts) *Manifest
	Watchdog(manifest *Manifest, out *output) error
}

// Manifest describes a list of services and their dependencies
type Manifest struct {
	ctx *ExContext

	// list of services
	services []*service

	// overrides is a map of service name to the path of the executable to run
	// on the host machine instead of a container.
	overrides map[string]string

	out *output
}

func NewManifest(ctx *ExContext, out *output) *Manifest {
	return &Manifest{ctx: ctx, out: out, overrides: make(map[string]string)}
}

type LogLevel string

var (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelTrace LogLevel = "trace"
)

func (l *LogLevel) Unmarshal(s string) error {
	switch s {
	case "debug":
		*l = LevelDebug
	case "info":
		*l = LevelInfo
	case "trace":
		*l = LevelTrace
	case "warn":
		*l = LevelWarn
	case "error":
		*l = LevelError
	default:
		return fmt.Errorf("invalid log level: %s", s)
	}
	return nil
}

// Execution context
type ExContext struct {
	LogLevel LogLevel
}

type Service interface {
	Run(service *service, ctx *ExContext)
}

type ServiceReady interface {
	Ready(out io.Writer, service *service, ctx context.Context) error
}

func WaitForReady(manifest *Manifest) error {
	var wg sync.WaitGroup
	readyErr := make(chan error, len(manifest.Services()))

	output, err := manifest.out.LogOutput("ready")
	if err != nil {
		return fmt.Errorf("failed to create log output: %w", err)
	}

	for _, s := range manifest.Services() {
		if readyFn, ok := s.component.(ServiceReady); ok {
			wg.Add(1)

			go func() {
				defer wg.Done()

				if err := readyFn.Ready(output, s, context.Background()); err != nil {
					readyErr <- fmt.Errorf("service %s failed to start: %w", s.Name, err)
				}
			}()
		}
	}
	wg.Wait()

	close(readyErr)
	for err := range readyErr {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Manifest) Services() []*service {
	return s.services
}

// ReleaseService is a service that can also be runned as an artifact in the host machine
type ReleaseService interface {
	ReleaseArtifact() *release
}

func (s *Manifest) AddService(name string, srv Service) {
	service := s.NewService(name)
	service.component = srv
	srv.Run(service, s.ctx)

	s.services = append(s.services, service)
}

func (s *Manifest) MustGetService(name string) *service {
	service, ok := s.GetService(name)
	if !ok {
		panic(fmt.Sprintf("service %s not found", name))
	}
	return service
}

func (s *Manifest) GetService(name string) (*service, bool) {
	for _, ss := range s.services {
		if ss.Name == name {
			return ss, true
		}
	}
	return nil, false
}

// Validate validates the manifest
// - checks if all the port dependencies are met from the service description
// - downloads any local release artifacts for the services that require host execution
func (s *Manifest) Validate() error {
	for _, ss := range s.services {
		for _, nodeRef := range ss.nodeRefs {
			targetService, ok := s.GetService(nodeRef.Service)
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.Name, nodeRef.Service)
			}

			if _, ok := targetService.GetPort(nodeRef.PortLabel); !ok {
				return fmt.Errorf("service %s depends on service %s, but it does not expose port %s", ss.Name, nodeRef.Service, nodeRef.PortLabel)
			}
		}
	}

	// download any local release artifacts for the services that require them
	for _, ss := range s.services {
		if ss.labels[useHostExecutionLabel] == "true" {
			// If the service wants to run on the host, it must implement the ReleaseService interface
			// which provides functions to download the release artifact.
			releaseService, ok := ss.component.(ReleaseService)
			if !ok {
				return fmt.Errorf("service '%s' must implement the ReleaseService interface", ss.Name)
			}
			releaseArtifact := releaseService.ReleaseArtifact()
			bin, err := downloadRelease(s.out.homeDir, releaseArtifact)
			if err != nil {
				return fmt.Errorf("failed to download release artifact for service '%s': %w", ss.Name, err)
			}
			s.overrides[ss.Name] = bin
		}
	}
	return nil
}

// Port describes a port that a service exposes
type Port struct {
	// Name is the name of the port
	Name string

	// Port is the port number
	Port int

	// HostPort is the port number assigned on the host machine for this
	// container port. It is populated by the local runner
	// TODO: We might want to move this to the runner itself.
	HostPort int
}

// NodeRef describes a reference from one service to another
type NodeRef struct {
	Service   string
	PortLabel string
}

type service struct {
	Name string
	args []string

	labels map[string]string

	ports    []*Port
	nodeRefs []*NodeRef

	tag        string
	image      string
	entrypoint string

	component Service
}

func (s *service) Ports() []*Port {
	return s.ports
}

func (s *service) MustGetPort(name string) *Port {
	port, ok := s.GetPort(name)
	if !ok {
		panic(fmt.Sprintf("port %s not found", name))
	}
	return port
}

func (s *service) GetPort(name string) (*Port, bool) {
	for _, p := range s.ports {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

func (s *service) UseHostExecution() *service {
	s.WithLabel(useHostExecutionLabel, "true")
	return s
}

func (s *service) WithLabel(key, value string) *service {
	if s.labels == nil {
		s.labels = make(map[string]string)
	}
	s.labels[key] = value
	return s
}

func (s *Manifest) NewService(name string) *service {
	return &service{Name: name, args: []string{}, ports: []*Port{}, nodeRefs: []*NodeRef{}}
}

func (s *service) WithImage(image string) *service {
	s.image = image
	return s
}

func (s *service) WithEntrypoint(entrypoint string) *service {
	s.entrypoint = entrypoint
	return s
}

func (s *service) WithTag(tag string) *service {
	s.tag = tag
	return s
}

func (s *service) WithPort(name string, portNumber int) *service {
	// add the port if not already present with the same name.
	// if preset with the same name, they must have same port number
	for _, p := range s.ports {
		if p.Name == name {
			if p.Port != portNumber {
				panic(fmt.Sprintf("port %s already defined with different port number", name))
			}
			return s
		}
	}
	s.ports = append(s.ports, &Port{Name: name, Port: portNumber})
	return s
}

func (s *service) WithArgs(args ...string) *service {
	for i, arg := range args {
		var port []Port
		var nodeRef []NodeRef
		args[i], port, nodeRef = applyTemplate(arg)
		for _, p := range port {
			s.WithPort(p.Name, p.Port)
		}
		for _, n := range nodeRef {
			s.nodeRefs = append(s.nodeRefs, &n)
		}
	}
	s.args = append(s.args, args...)
	return s
}

func applyTemplate(templateStr string) (string, []Port, []NodeRef) {
	// use template substitution to load constants
	// pass-through the Dir template because it has to be resolved at the runtime
	input := map[string]interface{}{
		"Dir": "{{.Dir}}",
	}

	var portRef []Port
	var nodeRef []NodeRef
	// ther can be multiple port and nodere because in the case of op-geth we pass a whole string as nested command args

	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
			if name == "" {
				panic("BUG: service name cannot be empty")
			}
			if portLabel == "" {
				panic("BUG: port label cannot be empty")
			}

			// for the first pass of service we do not do anything, keep it as it is for the followup pass
			// here we only keep the references to the services to be checked if they are valid and an be resolved
			// later on for the runtime we will do the resolve stage.
			// TODO: this will get easier when we move away from templates and use interface and structs.
			nodeRef = append(nodeRef, NodeRef{Service: name, PortLabel: portLabel})
			return fmt.Sprintf(`{{Service "%s" "%s"}}`, name, portLabel)
		},
		"Port": func(name string, defaultPort int) string {
			portRef = append(portRef, Port{Name: name, Port: defaultPort})
			return fmt.Sprintf(`{{Port "%s" %d}}`, name, defaultPort)
		},
	}

	tpl, err := template.New("").Funcs(funcs).Parse(templateStr)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to parse template, err: %s", err))
	}

	var out strings.Builder
	if err := tpl.Execute(&out, input); err != nil {
		panic(fmt.Sprintf("BUG: failed to execute template, err: %s", err))
	}
	res := out.String()

	// escape quotes
	res = strings.ReplaceAll(res, `&#34;`, `"`)

	return res, portRef, nodeRef
}

func (s *Manifest) GenerateDotGraph() string {
	var b strings.Builder
	b.WriteString("digraph G {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=record];\n\n")

	// Create a map of services for easy lookup
	servicesMap := make(map[string]*service)
	for _, ss := range s.services {
		servicesMap[ss.Name] = ss
	}

	// Add nodes (services) with their ports as labels
	for _, ss := range s.services {
		var ports []string
		for _, p := range ss.ports {
			ports = append(ports, fmt.Sprintf("%s:%d", p.Name, p.Port))
		}
		portLabel := ""
		if len(ports) > 0 {
			portLabel = "|{" + strings.Join(ports, "|") + "}"
		}
		// Replace hyphens with underscores for DOT compatibility
		nodeName := strings.ReplaceAll(ss.Name, "-", "_")
		b.WriteString(fmt.Sprintf("  %s [label=\"%s%s\"];\n", nodeName, ss.Name, portLabel))
	}

	b.WriteString("\n")

	// Add edges (connections between services)
	for _, ss := range s.services {
		sourceNode := strings.ReplaceAll(ss.Name, "-", "_")
		for _, ref := range ss.nodeRefs {
			targetNode := strings.ReplaceAll(ref.Service, "-", "_")
			b.WriteString(fmt.Sprintf("  %s -> %s [label=\"%s\"];\n",
				sourceNode,
				targetNode,
				ref.PortLabel,
			))
		}
	}

	b.WriteString("}\n")
	return b.String()
}

func saveDotGraph(svcManager *Manifest, out *output) error {
	dotGraph := svcManager.GenerateDotGraph()
	return out.WriteFile("services.dot", dotGraph)
}
