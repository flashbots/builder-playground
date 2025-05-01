package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	flag "github.com/spf13/pflag"
)

const useHostExecutionLabel = "use-host-execution"

type Recipe interface {
	Name() string
	Description() string
	Flags() *flag.FlagSet
	Artifacts() *ArtifactsBuilder
	Apply(ctx *ExContext, artifacts *Artifacts) *Manifest
	Output(manifest *Manifest) map[string]interface{}
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
	Name() string
}

type ServiceReady interface {
	Ready(ouservice *service) error
}

func (m *Manifest) CompleteReady() error {
	for _, s := range m.services {
		if readyFn, ok := s.component.(ServiceReady); ok {
			if err := readyFn.Ready(s); err != nil {
				return err
			}
		}
	}
	return nil
}

type ServiceWatchdog interface {
	Watchdog(out io.Writer, service *service, ctx context.Context) error
}

func RunWatchdog(manifest *Manifest) error {
	watchdogErr := make(chan error, len(manifest.Services()))

	output, err := manifest.out.LogOutput("watchdog")
	if err != nil {
		return fmt.Errorf("failed to create log output: %w", err)
	}

	for _, s := range manifest.Services() {
		if watchdogFn, ok := s.component.(ServiceWatchdog); ok {
			go func() {
				if err := watchdogFn.Watchdog(output, s, context.Background()); err != nil {
					watchdogErr <- fmt.Errorf("service %s watchdog failed: %w", s.Name, err)
				}
			}()
		}
	}

	// If any of the watchdogs fail, we return the error
	if err := <-watchdogErr; err != nil {
		return fmt.Errorf("failed to run watchdog: %w", err)
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
		// validate node port references
		for _, nodeRef := range ss.nodeRefs {
			targetService, ok := s.GetService(nodeRef.Service)
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.Name, nodeRef.Service)
			}

			if _, ok := targetService.GetPort(nodeRef.PortLabel); !ok {
				return fmt.Errorf("service %s depends on service %s, but it does not expose port %s", ss.Name, nodeRef.Service, nodeRef.PortLabel)
			}
		}

		// validate depends_on statements
		for _, dep := range ss.dependsOn {
			service, ok := s.GetService(dep.Name)
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.Name, dep.Name)
			}

			if dep.Condition == DependsOnConditionHealthy {
				// if we depedn on the service to be healthy, it must have a ready check
				if service.readyCheck == nil {
					return fmt.Errorf("service %s depends on service %s, but it does not have a ready check", ss.Name, dep.Name)
				}
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
			bin, err := DownloadRelease(s.out.homeDir, releaseArtifact)
			if err != nil {
				return fmt.Errorf("failed to download release artifact for service '%s': %w", ss.Name, err)
			}
			s.overrides[ss.Name] = bin
		}
	}

	// validate that the mounts are correct
	for _, ss := range s.services {
		for _, fileNameRef := range ss.filesMapped {
			fileLoc := filepath.Join(s.out.dst, fileNameRef)

			if _, err := os.Stat(fileLoc); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("service %s includes an unknown file %s does not exist", ss.Name, fileLoc)
				}
				return fmt.Errorf("failed to stat file %s: %w", fileLoc, err)
			}
		}
	}

	return nil
}

const (
	ProtocolUDP = "udp"
	ProtocolTCP = "tcp"
)

// Port describes a port that a service exposes
type Port struct {
	// Name is the name of the port
	Name string

	// Port is the port number
	Port int

	// Protocol (tcp or udp)
	Protocol string

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

// serviceLogs is a service to access the logs of the running service
type serviceLogs struct {
	path string
}

func (s *serviceLogs) readLogs() (string, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}
	return string(content), nil
}

func (s *serviceLogs) FindLog(pattern string) (string, error) {
	logs, err := s.readLogs()
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	lines := strings.Split(logs, "\n")
	for _, line := range lines {
		if strings.Contains(line, pattern) {
			return line, nil
		}
	}
	return "", fmt.Errorf("log pattern %s not found", pattern)
}

type service struct {
	Name string
	args []string

	labels map[string]string

	// list of environment variables to set for the service
	env map[string]string

	readyCheck *ReadyCheck

	dependsOn []DependsOn

	ports    []*Port
	nodeRefs []*NodeRef

	filesMapped   map[string]string
	volumesMapped map[string]string

	tag        string
	image      string
	entrypoint string

	logs      *serviceLogs
	component Service
}

type DependsOnCondition string

const (
	DependsOnConditionRunning DependsOnCondition = "service_started"
	DependsOnConditionHealthy DependsOnCondition = "service_healthy"
)

type DependsOn struct {
	Name      string
	Condition DependsOnCondition
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

func (s *service) WithEnv(key, value string) *service {
	if s.env == nil {
		s.env = make(map[string]string)
	}
	s.applyTemplate(value)
	s.env[key] = value
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

func (s *service) WithPort(name string, portNumber int, protocolVar ...string) *service {
	protocol := ProtocolTCP
	if len(protocolVar) > 0 {
		if protocolVar[0] != ProtocolTCP && protocolVar[0] != ProtocolUDP {
			panic(fmt.Sprintf("protocol %s not supported", protocolVar[0]))
		}
		protocol = protocolVar[0]
	}

	// add the port if not already present with the same name.
	// if preset with the same name, they must have same port number
	for _, p := range s.ports {
		if p.Name == name {
			if p.Port != portNumber {
				panic(fmt.Sprintf("port %s already defined with different port number", name))
			}
			if p.Protocol != protocol {
				// If they have different protocols they are different ports
				continue
			}
			return s
		}
	}
	s.ports = append(s.ports, &Port{Name: name, Port: portNumber, Protocol: protocol})
	return s
}

func (s *service) applyTemplate(arg string) {
	var port []Port
	var nodeRef []NodeRef
	_, port, nodeRef = applyTemplate(arg)
	for _, p := range port {
		s.WithPort(p.Name, p.Port, p.Protocol)
	}
	for _, n := range nodeRef {
		s.nodeRefs = append(s.nodeRefs, &n)
	}
}

func (s *service) WithArgs(args ...string) *service {
	for _, arg := range args {
		s.applyTemplate(arg)
	}
	s.args = append(s.args, args...)
	return s
}

func (s *service) WithVolume(name string, localPath string) *service {
	if s.volumesMapped == nil {
		s.volumesMapped = make(map[string]string)
	}
	s.volumesMapped[localPath] = name
	return s
}

func (s *service) WithArtifact(artifactName string, localPath string) *service {
	if s.filesMapped == nil {
		s.filesMapped = make(map[string]string)
	}
	s.filesMapped[localPath] = artifactName
	return s
}

func (s *service) WithReady(check ReadyCheck) *service {
	s.readyCheck = &check
	return s
}

type ReadyCheck struct {
	QueryURL    string
	Test        []string
	Interval    time.Duration
	StartPeriod time.Duration
	Timeout     time.Duration
	Retries     int
}

func (s *service) DependsOnHealthy(name string) *service {
	s.dependsOn = append(s.dependsOn, DependsOn{Name: name, Condition: DependsOnConditionHealthy})
	return s
}

func (s *service) DependsOnRunning(name string) *service {
	s.dependsOn = append(s.dependsOn, DependsOn{Name: name, Condition: DependsOnConditionRunning})
	return s
}

func applyTemplate(templateStr string) (string, []Port, []NodeRef) {
	// TODO: Can we remove the return argument string?

	// use template substitution to load constants
	// pass-through the Dir template because it has to be resolved at the runtime
	input := map[string]interface{}{
		"Dir": "{{.Dir}}",
	}

	var portRef []Port
	var nodeRef []NodeRef
	// ther can be multiple port and nodere because in the case of op-geth we pass a whole string as nested command args

	funcs := template.FuncMap{
		"Service": func(name string, portLabel, protocol string) string {
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
			portRef = append(portRef, Port{Name: name, Port: defaultPort, Protocol: ProtocolTCP})
			return fmt.Sprintf(`{{Port "%s" %d}}`, name, defaultPort)
		},
		"PortUDP": func(name string, defaultPort int) string {
			portRef = append(portRef, Port{Name: name, Port: defaultPort, Protocol: ProtocolUDP})
			return fmt.Sprintf(`{{PortUDP "%s" %d}}`, name, defaultPort)
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

	// Add edges for dependws_on
	for _, ss := range s.services {
		for _, dep := range ss.dependsOn {
			sourceNode := strings.ReplaceAll(ss.Name, "-", "_")
			targetNode := strings.ReplaceAll(dep.Name, "-", "_")
			b.WriteString(fmt.Sprintf("  %s -> %s [style=dashed, color=gray, constraint=true, label=\"depends_on\"];\n",
				sourceNode,
				targetNode,
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
