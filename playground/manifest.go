package playground

import (
	"fmt"
	"log"
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
	Apply(ctx *ExContext, artifacts *Artifacts) (*Manifest, error)
	Output(manifest *Manifest) map[string]interface{}
}

// Manifest describes a list of services and their dependencies
type Manifest struct {
	ctx *ExContext

	// list of services
	services []*Service

	// overrides is a map of service name to the path of the executable to run
	// on the host machine instead of a container.
	overrides map[string]string

	out *output
}

func NewManifest(ctx *ExContext, out *output) *Manifest {
	ctx.Output = out
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

	// This dependency is not ideal. Doing it so that I do not
	// have to modify the serviceDesc interface to give services
	// access to the output.
	Output       *output
	AlloyEnabled bool
	CaddyEnabled bool
}

type ServiceGen interface {
	Run(service *Service, ctx *ExContext)
	Name() string
}

type ServiceReady interface {
	Ready(instance *instance) error
}

func (s *Manifest) Services() []*Service {
	return s.services
}

// ReleaseService is a service that can also be runned as an artifact in the host machine
type ReleaseService interface {
	ReleaseArtifact() *release
}

func (s *Manifest) AddService(name string, srv ServiceGen) {
	service := s.NewService(name)
	service.ComponentName = srv.Name()
	srv.Run(service, s.ctx)

	s.services = append(s.services, service)
}

func (s *Manifest) MustGetService(name string) *Service {
	service, ok := s.GetService(name)
	if !ok {
		panic(fmt.Sprintf("service %s not found", name))
	}
	return service
}

func (s *Manifest) GetService(name string) (*Service, bool) {
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
		for _, nodeRef := range ss.NodeRefs {
			targetService, ok := s.GetService(nodeRef.Service)
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.Name, nodeRef.Service)
			}

			if _, ok := targetService.GetPort(nodeRef.PortLabel); !ok {
				return fmt.Errorf("service %s depends on service %s, but it does not expose port %s", ss.Name, nodeRef.Service, nodeRef.PortLabel)
			}
		}

		// validate depends_on statements
		for _, dep := range ss.DependsOn {
			service, ok := s.GetService(dep.Name)
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.Name, dep.Name)
			}

			if dep.Condition == DependsOnConditionHealthy {
				// if we depedn on the service to be healthy, it must have a ready check
				if service.ReadyCheck == nil {
					return fmt.Errorf("service %s depends on service %s, but it does not have a ready check", ss.Name, dep.Name)
				}
			}
		}
	}

	// validate that the mounts are correct
	for _, ss := range s.services {
		for _, fileNameRef := range ss.FilesMapped {
			fileLoc := filepath.Join(s.out.dst, fileNameRef)

			if _, err := os.Stat(fileLoc); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("service %s includes an unknown file %s does not exist", ss.Name, fileLoc)
				}
				return fmt.Errorf("failed to stat file %s: %w", fileLoc, err)
			}
		}
	}

	// validate that the mounts are correct
	for _, ss := range s.services {
		for _, fileNameRef := range ss.FilesMapped {
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
	Protocol  string
	User      string
}

// serviceLogs is a service to access the logs of the running service
type serviceLogs struct {
	logRef *os.File
	path   string
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

type Service struct {
	Name string   `json:"name"`
	Args []string `json:"args"`

	Labels map[string]string `json:"labels,omitempty"`

	// list of environment variables to set for the service
	Env map[string]string `json:"env,omitempty"`

	ReadyCheck *ReadyCheck `json:"ready_check,omitempty"`

	DependsOn []DependsOn `json:"depends_on,omitempty"`

	Ports    []*Port    `json:"ports,omitempty"`
	NodeRefs []*NodeRef `json:"node_refs,omitempty"`

	FilesMapped   map[string]string `json:"files_mapped,omitempty"`
	VolumesMapped map[string]string `json:"volumes_mapped,omitempty"`

	ComponentName string `json:"component_name,omitempty"`

	Tag        string `json:"tag,omitempty"`
	Image      string `json:"image,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`

	Privileged bool `json:"privileged,omitempty"`
}

type instance struct {
	service *Service

	logs      *serviceLogs
	component ServiceGen
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

func (s *Service) GetPorts() []*Port {
	return s.Ports
}

func (s *Service) MustGetPort(name string) *Port {
	port, ok := s.GetPort(name)
	if !ok {
		panic(fmt.Sprintf("port %s not found", name))
	}
	return port
}

func (s *Service) GetPort(name string) (*Port, bool) {
	for _, p := range s.Ports {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

func (s *Service) UseHostExecution() *Service {
	s.WithLabel(useHostExecutionLabel, "true")
	return s
}

func (s *Service) WithEnv(key, value string) *Service {
	if s.Env == nil {
		s.Env = make(map[string]string)
	}
	s.applyTemplate(value)
	s.Env[key] = value
	return s
}

func (s *Service) WithLabel(key, value string) *Service {
	if s.Labels == nil {
		s.Labels = make(map[string]string)
	}
	s.Labels[key] = value
	return s
}

func (s *Manifest) NewService(name string) *Service {
	return &Service{Name: name, Args: []string{}, Ports: []*Port{}, NodeRefs: []*NodeRef{}}
}

func (s *Service) WithImage(image string) *Service {
	s.Image = image
	return s
}

func (s *Service) WithEntrypoint(entrypoint string) *Service {
	s.Entrypoint = entrypoint
	return s
}

func (s *Service) WithTag(tag string) *Service {
	s.Tag = tag
	return s
}

func (s *Service) WithPort(name string, portNumber int, protocolVar ...string) *Service {
	protocol := ProtocolTCP
	if len(protocolVar) > 0 {
		if protocolVar[0] != ProtocolTCP && protocolVar[0] != ProtocolUDP {
			panic(fmt.Sprintf("protocol %s not supported", protocolVar[0]))
		}
		protocol = protocolVar[0]
	}

	// add the port if not already present with the same name.
	// if preset with the same name, they must have same port number
	for _, p := range s.Ports {
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
	s.Ports = append(s.Ports, &Port{Name: name, Port: portNumber, Protocol: protocol})
	return s
}

func (s *Service) applyTemplate(arg string) {
	var port []Port
	var nodeRef []NodeRef
	_, port, nodeRef = applyTemplate(arg)
	for _, p := range port {
		s.WithPort(p.Name, p.Port, p.Protocol)
	}
	for _, n := range nodeRef {
		s.NodeRefs = append(s.NodeRefs, &n)
	}
}

func (s *Service) WithArgs(args ...string) *Service {
	for _, arg := range args {
		s.applyTemplate(arg)
	}
	s.Args = append(s.Args, args...)
	return s
}

func (s *Service) WithPrivileged() *Service {
	log.Printf("Service %s is privileged. This is not recommended for production use.", s.Name)
	s.Privileged = true
	return s
}

func (s *Service) WithVolume(name string, localPath string) *Service {
	if s.VolumesMapped == nil {
		s.VolumesMapped = make(map[string]string)
	}
	s.VolumesMapped[localPath] = name
	return s
}

// WithAbsoluteVolume adds a volume mapping using an absolute path on the host.
// This is useful for binding system paths like /var/run/docker.sock.
// The path must be absolute and will be used as-is without any modification.
func (s *Service) WithAbsoluteVolume(containerPath string, hostPath string) *Service {
	if !filepath.IsAbs(hostPath) {
		panic(fmt.Sprintf("host path must be absolute: %s", hostPath))
	}
	if s.VolumesMapped == nil {
		s.VolumesMapped = make(map[string]string)
	}
	s.VolumesMapped[containerPath] = hostPath
	return s
}

func (s *Service) WithArtifact(localPath string, artifactName string) *Service {
	if s.FilesMapped == nil {
		s.FilesMapped = make(map[string]string)
	}
	s.FilesMapped[localPath] = artifactName
	return s
}

func (s *Service) WithReady(check ReadyCheck) *Service {
	s.ReadyCheck = &check
	return s
}

type ReadyCheck struct {
	QueryURL    string        `json:"query_url"`
	Test        []string      `json:"test"`
	Interval    time.Duration `json:"interval"`
	StartPeriod time.Duration `json:"start_period"`
	Timeout     time.Duration `json:"timeout"`
	Retries     int           `json:"retries"`
}

func (s *Service) DependsOnHealthy(name string) *Service {
	s.DependsOn = append(s.DependsOn, DependsOn{Name: name, Condition: DependsOnConditionHealthy})
	return s
}

func (s *Service) DependsOnRunning(name string) *Service {
	s.DependsOn = append(s.DependsOn, DependsOn{Name: name, Condition: DependsOnConditionRunning})
	return s
}

func applyTemplate(templateStr string) (string, []Port, []NodeRef) {
	// TODO: Can we remove the return argument string?

	// use template substitution to load constants
	// pass-through the Dir template because it has to be resolved at the runtime
	input := map[string]interface{}{}

	var portRef []Port
	var nodeRef []NodeRef
	// ther can be multiple port and nodere because in the case of op-geth we pass a whole string as nested command args

	funcs := template.FuncMap{
		"Service": func(name string, portLabel, protocol, user string) string {
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
			nodeRef = append(nodeRef, NodeRef{Service: name, PortLabel: portLabel, Protocol: protocol, User: user})
			return fmt.Sprintf(`{{Service "%s" "%s" "%s" "%s"}}`, name, portLabel, protocol, user)
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
	servicesMap := make(map[string]*Service)
	for _, ss := range s.services {
		servicesMap[ss.Name] = ss
	}

	// Add nodes (services) with their ports as labels
	for _, ss := range s.services {
		var ports []string
		for _, p := range ss.Ports {
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
		for _, ref := range ss.NodeRefs {
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
		for _, dep := range ss.DependsOn {
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

func (m *Manifest) SaveJson() error {
	format := map[string]interface{}{
		"services": m.services,
	}
	return m.out.WriteFile("manifest.json", format)
}
