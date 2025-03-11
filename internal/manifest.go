package internal

import (
	"fmt"
	"slices"
	"strings"
	"text/template"

	flag "github.com/spf13/pflag"
)

type Recipe interface {
	Name() string
	Flags() *flag.FlagSet
	Artifacts() *ArtifactsBuilder
	Apply(artifacts *Artifacts) *Manifest
	Watchdog(manifest *Manifest) error
}

type Manifest struct {
	// list of services to start
	services []*service

	out *output
}

func NewManifest(out *output) *Manifest {
	return &Manifest{out: out}
}

type Service interface {
	Run(service *service)
}

func (s *Manifest) AddService(name string, srv Service) {
	service := s.NewService(name)
	srv.Run(service)

	s.services = append(s.services, service)
}

func (s *Manifest) GetService(name string) (*service, bool) {
	for _, ss := range s.services {
		if ss.name == name {
			return ss, true
		}
	}
	return nil, false
}

func (s *Manifest) Validate() error {
	// first, try to download all the artifacts
	// figure out if all the port dependencies are met from the service description
	servicesMap := make(map[string]*service)
	for _, ss := range s.services {
		servicesMap[ss.name] = ss
	}

	for _, ss := range s.services {
		for _, nodeRef := range ss.nodeRefs {
			targetService, ok := servicesMap[nodeRef.Service]
			if !ok {
				return fmt.Errorf("service %s depends on service %s, but it is not defined", ss.name, nodeRef.Service)
			}

			found := false
			for _, targetPort := range targetService.ports {
				if targetPort.name == nodeRef.PortLabel {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("service %s depends on service %s, but it does not expose port %s", ss.name, nodeRef.Service, nodeRef.PortLabel)
			}
		}
	}
	return nil
}

type port struct {
	name string
	port int

	// this is populated by the service manager
	hostPort int
}

type service struct {
	name string
	args []string

	ports    []*port
	nodeRefs []*NodeRef

	tag        string
	image      string
	entrypoint string
}

func (s *service) GetPort(name string) (*port, bool) {
	for _, p := range s.ports {
		if p.name == name {
			return p, true
		}
	}
	return nil, false
}

func (s *Manifest) NewService(name string) *service {
	return &service{name: name, args: []string{}, ports: []*port{}, nodeRefs: []*NodeRef{}}
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
		if p.name == name {
			if p.port != portNumber {
				panic(fmt.Sprintf("port %s already defined with different port number", name))
			}
			return s
		}
	}
	s.ports = append(s.ports, &port{name: name, port: portNumber})
	return s
}

func (s *service) WithArgs(args ...string) *service {
	for i, arg := range args {
		var port []port
		var nodeRef []NodeRef
		args[i], port, nodeRef = applyTemplate(arg)
		for _, p := range port {
			s.WithPort(p.name, p.port)
		}
		for _, n := range nodeRef {
			s.nodeRefs = append(s.nodeRefs, &n)
		}
	}
	s.args = append(s.args, args...)
	return s
}

// WithReplacementArgs finds the first occurrence of the first argument in the current arguments,
// and replaces it and len(args) - 1 more arguments with the new arguments.
//
// For example:
//
// s.WithArgs("a", "b", "c").WithReplacementArgs("b", "d") will result in ["a", "b", "d"]
func (s *service) WithReplacementArgs(args ...string) *service {
	if len(args) == 0 {
		return s
	}
	// use template substitution to load constants
	for i, arg := range args {
		// skip refs since we do not do them yet on replacement args
		args[i], _, _ = applyTemplate(arg)
	}

	if i := slices.Index(s.args, args[0]); i != -1 {
		s.args = slices.Replace(s.args, i, i+len(args), args...)
	} else {
		s.args = append(s.args, args...)
	}
	return s
}

func (s *service) If(cond bool, fn func(*service) *service) *service {
	if cond {
		return fn(s)
	}
	return s
}

func applyTemplate(templateStr string) (string, []port, []NodeRef) {
	// use template substitution to load constants
	// pass-through the Dir template because it has to be resolved at the runtime
	input := map[string]interface{}{
		"Dir": "{{.Dir}}",
	}

	var portRef []port
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
			portRef = append(portRef, port{name: name, port: defaultPort})
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
		servicesMap[ss.name] = ss
	}

	// Add nodes (services) with their ports as labels
	for _, ss := range s.services {
		var ports []string
		for _, p := range ss.ports {
			ports = append(ports, fmt.Sprintf("%s:%d", p.name, p.port))
		}
		portLabel := ""
		if len(ports) > 0 {
			portLabel = "|{" + strings.Join(ports, "|") + "}"
		}
		// Replace hyphens with underscores for DOT compatibility
		nodeName := strings.ReplaceAll(ss.name, "-", "_")
		b.WriteString(fmt.Sprintf("  %s [label=\"%s%s\"];\n", nodeName, ss.name, portLabel))
	}

	b.WriteString("\n")

	// Add edges (connections between services)
	for _, ss := range s.services {
		sourceNode := strings.ReplaceAll(ss.name, "-", "_")
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
