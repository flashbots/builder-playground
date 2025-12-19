package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/flashbots/builder-playground/utils"
	flag "github.com/spf13/pflag"
)

const useHostExecutionLabel = "use-host-execution"

type Recipe interface {
	Name() string
	Description() string
	Flags() *flag.FlagSet
	Artifacts() *ArtifactsBuilder
	Apply(manifest *Manifest)
	Output(manifest *Manifest) map[string]interface{}
}

// Manifest describes a list of services and their dependencies
type Manifest struct {
	ctx *ExContext
	ID  string `json:"session_id"`

	// list of Services
	Services []*Service `json:"services"`

	// overrides is a map of service name to the path of the executable to run
	// on the host machine instead of a container.
	overrides map[string]string

	out *output
}

func (m *Manifest) ApplyOverrides(overrides map[string]string) error {
	// Now, the override can either be one of two things (we are overloading the override map):
	// - docker image: In that case, change the manifest and remove from override map
	// - a path to an executable: In that case, we need to run it on the host machine
	// and use the override map <- We only check this case, and if it is not a path, we assume
	// it is a docker image. If it is not a docker image either, the error will be catched during the execution
	for k, v := range overrides {
		srv, ok := m.GetService(k)
		if !ok {
			return fmt.Errorf("service '%s' not found", k)
		}

		if _, err := os.Stat(v); err == nil {
			srv.HostPath = v
		} else {
			// this is a path to an executable, remove it from the overrides since we
			// assume it s a docker image and add it to manifest
			parts := strings.Split(v, ":")
			if len(parts) != 2 {
				return fmt.Errorf("invalid override docker image %s, expected image:tag", v)
			}

			srv.Image = parts[0]
			srv.Tag = parts[1]
		}
	}

	return nil
}

func NewManifest(ctx *ExContext, out *output) *Manifest {
	ctx.Output = out
	return &Manifest{
		ID:        utils.GeneratePetName(),
		ctx:       ctx,
		out:       out,
		overrides: make(map[string]string),
	}
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

type ContenderContext struct {
	// Run `contender spam` automatically once all playground services are running.
	Enabled bool

	// Provide additional args to contender's CLI.
	ExtraArgs []string

	// Override the default target chain for contender to spam.
	TargetChain string
}

// Execution context
type ExContext struct {
	LogLevel LogLevel

	// This dependency is not ideal. Doing it so that I do not
	// have to modify the serviceDesc interface to give services
	// access to the output.
	Output *output

	// Bootnode reference for EL nodes.
	// TODO: Extend for CL nodes too
	Bootnode *BootnodeRef

	Contender *ContenderContext
}

type BootnodeRef struct {
	Service string
	ID      string
}

func (b *BootnodeRef) Connect() string {
	return ConnectEnode(b.Service, b.ID)
}

type ServiceGen interface {
	Apply(manifest *Manifest)
}

type ServiceReady interface {
	Ready(service *Service) error
}

func (s *Manifest) AddService(srv ServiceGen) {
	srv.Apply(s)
}

func (s *Manifest) MustGetService(name string) *Service {
	service, ok := s.GetService(name)
	if !ok {
		panic(fmt.Sprintf("service %s not found", name))
	}
	return service
}

func (s *Manifest) GetService(name string) (*Service, bool) {
	for _, ss := range s.Services {
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
	for _, ss := range s.Services {
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
	for _, ss := range s.Services {
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
	for _, ss := range s.Services {
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
	Name string `json:"name"`

	// Port is the port number
	Port int `json:"port"`

	// Protocol (tcp or udp)
	Protocol string

	// HostPort is the port number assigned on the host machine for this
	// container port. It is populated by the local runner
	// TODO: We might want to move this to the runner itself.
	HostPort int
}

// NodeRef describes a reference from one service to another
type NodeRef struct {
	Service   string `json:"service"`
	PortLabel string `json:"port_label"`
	Protocol  string `json:"protocol"`
	User      string `json:"user"`
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

	Tag        string `json:"tag,omitempty"`
	Image      string `json:"image,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
	HostPath   string `json:"host_path,omitempty"`

	release    *release
	watchdogFn watchdogFn
	readyFn    readyFn
}

type (
	watchdogFn func(out io.Writer, service *Service, ctx context.Context) error
	readyFn    func(ctx context.Context, service *Service) error
)

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
	ss := &Service{Name: name, Args: []string{}, Ports: []*Port{}, NodeRefs: []*NodeRef{}}
	s.Services = append(s.Services, ss)
	return ss
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

func (s *Service) WithRelease(rel *release) *Service {
	s.release = rel
	return s
}

func (s *Service) WithWatchdog(watchdogFn watchdogFn) *Service {
	s.watchdogFn = watchdogFn
	return s
}

func (s *Service) WithReadyFn(readyFn readyFn) *Service {
	s.readyFn = readyFn
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

func (s *Service) WithVolume(name, localPath string) *Service {
	if s.VolumesMapped == nil {
		s.VolumesMapped = make(map[string]string)
	}
	s.VolumesMapped[localPath] = name
	return s
}

func (s *Service) WithArtifact(localPath, artifactName string) *Service {
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
	UseNC       bool          `json:"use_nc"`
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
		"Service": func(name, portLabel, protocol, user string) string {
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
	for _, ss := range s.Services {
		servicesMap[ss.Name] = ss
	}

	// Add nodes (services) with their ports as labels
	for _, ss := range s.Services {
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
	for _, ss := range s.Services {
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
	for _, ss := range s.Services {
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

func (m *Manifest) SaveJson() error {
	return m.out.WriteFile("manifest.json", m)
}

func ReadManifest(outputFolder string) (*Manifest, error) {
	// read outputFolder/manifest.json file
	manifestFile := filepath.Join(outputFolder, "manifest.json")
	if _, err := os.Stat(manifestFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("manifest file %s does not exist", manifestFile)
	}
	manifest, err := os.ReadFile(manifestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %s: %w", manifestFile, err)
	}

	// parse the manifest file
	var manifestData Manifest
	if err := json.Unmarshal(manifest, &manifestData); err != nil {
		return nil, fmt.Errorf("failed to parse manifest file %s: %w", manifestFile, err)
	}

	// set the output folder
	manifestData.out = &output{
		dst: outputFolder,
	}
	return &manifestData, nil
}

func (svcManager *Manifest) RunContenderIfEnabled() {
	if svcManager.ctx.Contender.Enabled {
		svcManager.AddService(svcManager.ctx.Contender.Contender())
	}
}
