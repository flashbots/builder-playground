package internal

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"

	"github.com/prysmaticlabs/prysm/v5/config/params"
	flag "github.com/spf13/pflag"
)

type Recipe interface {
	Name() string
	Flags() *flag.FlagSet
	Artifacts() *ArtifactsBuilder
	Apply(artifacts *Artifacts) *serviceManager
	Watchdog(manifest *serviceManager) error
}

type serviceManager struct {
	// list of services to start
	services []*service

	out     *output
	handles []*handle

	stopping atomic.Bool

	wg sync.WaitGroup

	// channel for the handles to nofify when they are shutting down
	closeCh chan struct{}
}

func newServiceManager(out *output) *serviceManager {
	return &serviceManager{out: out, handles: []*handle{}, stopping: atomic.Bool{}, wg: sync.WaitGroup{}, closeCh: make(chan struct{}, 5)}
}

func (s *serviceManager) emitError() {
	select {
	case s.closeCh <- struct{}{}:
	default:
	}
}

func (s *serviceManager) Build(ss *service) {
	s.services = append(s.services, ss)
}

type Service interface {
	Run(service *service)
}

func (s *serviceManager) AddService(name string, srv Service) {
	service := s.NewService(name)
	srv.Run(service)
}

func (s *serviceManager) GetService(name string) (*service, bool) {
	for _, ss := range s.services {
		if ss.name == name {
			return ss, true
		}
	}
	return nil, false
}

func (s *serviceManager) Validate() {
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
				panic(fmt.Sprintf("service %s depends on service %s, but it is not defined", ss.name, nodeRef.Service))
			}

			found := false
			for _, targetPort := range targetService.ports {
				if targetPort.name == nodeRef.PortLabel {
					found = true
					break
				}
			}
			if !found {
				fmt.Println(targetService.ports)
				panic(fmt.Sprintf("service %s depends on service %s, but it does not expose port %s", ss.name, nodeRef.Service, nodeRef.PortLabel))
			}
		}
	}
}

func (s *serviceManager) runService(ss *service) {
	if ss.srvMng == nil {
		// this one was not created with Build so it is not a binary service, but a native one
		return
	}

	fmt.Println("Running", ss.imagePath, ss.args)
	cmd := exec.Command(ss.imagePath, ss.args...)

	logOutput, err := s.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	s.wg.Add(1)
	go func() {
		if err := cmd.Run(); err != nil {
			if !s.stopping.Load() {
				fmt.Printf("Error running %s: %v\n", ss.name, err)
			}
		}
		s.wg.Done()
		s.emitError()
	}()

	s.handles = append(s.handles, &handle{
		Process: cmd,
		Service: ss,
	})
}

type handle struct {
	Process *exec.Cmd
	Service *service
}

func (s *serviceManager) NotifyErrCh() <-chan struct{} {
	return s.closeCh
}

func (s *serviceManager) StopAndWait() {
	s.stopping.Store(true)

	for _, h := range s.handles {
		if h.Process != nil {
			fmt.Printf("Stopping %s\n", h.Service.name)
			h.Process.Process.Kill()
		}
	}
	s.wg.Wait()
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

	srvMng *serviceManager

	override string

	// release specific configuration
	// we call this image here but it can also represent a release binary
	image      string
	imagePath  string
	tag        string
	imageReal  string
	entrypoint string
}

func (s *service) GetPort(name string) *port {
	for _, p := range s.ports {
		if p.name == name {
			return p
		}
	}
	panic(fmt.Sprintf("BUG: port %s not found for service %s", name, s.name))
}

func (s *serviceManager) NewService(name string) *service {
	return &service{name: name, args: []string{}, srvMng: s, ports: []*port{}, nodeRefs: []*NodeRef{}}
}

func (s *service) WithImageReal(image string) *service {
	s.imageReal = image
	return s
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
	// use template substitution to load constants
	tmplVars := s.tmplVars()
	for i, arg := range args {
		var port []port
		var nodeRef []NodeRef
		args[i], port, nodeRef = applyTemplate(arg, tmplVars)
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

func (s *service) tmplVars() map[string]interface{} {
	tmplVars := map[string]interface{}{
		"Dir": s.srvMng.out.dst,
	}
	return tmplVars
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
	tmplVars := s.tmplVars()
	for i, arg := range args {
		// skip refs since we do not do them yet on replacement args
		args[i], _, _ = applyTemplate(arg, tmplVars)
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

func (s *service) Build() {
	s.srvMng.Build(s)
}

func applyTemplate(templateStr string, input interface{}) (string, []port, []NodeRef) {
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

func convert(config *params.BeaconChainConfig) ([]byte, error) {
	val := reflect.ValueOf(config).Elem()

	vals := []string{}
	for i := 0; i < val.NumField(); i++ {
		// only encode the public fields with tag 'yaml'
		tag := val.Type().Field(i).Tag.Get("yaml")
		if tag == "" {
			continue
		}

		// decode the type of the value
		typ := val.Field(i).Type()

		var resTyp string
		if isByteArray(typ) || isByteSlice(typ) {
			resTyp = "0x" + hex.EncodeToString(val.Field(i).Bytes())
		} else {
			// basic types
			switch typ.Kind() {
			case reflect.String:
				resTyp = val.Field(i).String()
			case reflect.Uint8, reflect.Uint64:
				resTyp = fmt.Sprintf("%d", val.Field(i).Uint())
			case reflect.Int:
				resTyp = fmt.Sprintf("%d", val.Field(i).Int())
			default:
				panic(fmt.Sprintf("BUG: unsupported type, tag '%s', err: '%s'", tag, val.Field(i).Kind()))
			}
		}

		vals = append(vals, fmt.Sprintf("%s: %s", tag, resTyp))
	}

	return []byte(strings.Join(vals, "\n")), nil
}

func isByteArray(t reflect.Type) bool {
	return t.Kind() == reflect.Array && t.Elem().Kind() == reflect.Uint8
}

func isByteSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

var prefundedAccounts = []string{
	"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
	"0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
	"0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
	"0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
	"0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
	"0x8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
	"0x92db14e403b83dfe3df233f83dfa3a0d7096f21ca9b0d6d6b8d88b2b4ec1564e",
	"0x4bbbf85ce3377467afe5d46f804f221813b2bb87f24d81f60f1fcdbf7cbf4356",
	"0xdbda1821b80551c9d65939329250298aa3472ba22feea921c0cf5d620ea67b97",
	"0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
}

func getHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("error getting user home directory: %w", err)
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(homeDir, ".playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0755); err != nil {
		return "", fmt.Errorf("error creating output directory: %v", err)
	}

	return customHomeDir, nil
}

func (s *serviceManager) GenerateDotGraph() string {
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

func saveDotGraph(svcManager *serviceManager, out *output) error {
	dotGraph := svcManager.GenerateDotGraph()
	return out.WriteFile("services.dot", dotGraph)
}
