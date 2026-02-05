package playground

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	flag "github.com/spf13/pflag"
)

// YAMLRecipeConfig represents the structure of a YAML recipe file
type YAMLRecipeConfig struct {
	// Base is the name of the base recipe (l1, opstack, buildernet)
	Base string `yaml:"base"`

	// Description is an optional description of the recipe
	Description string `yaml:"description,omitempty"`

	// Recipe contains the component/service hierarchy to apply as overrides or additions
	Recipe map[string]*YAMLComponentConfig `yaml:"recipe"`
}

// YAMLComponentConfig represents a component in the YAML recipe
type YAMLComponentConfig struct {
	// Remove indicates whether to remove this component
	Remove bool `yaml:"remove,omitempty"`

	// Services is a map of service name to service config
	Services map[string]*YAMLServiceConfig `yaml:"services,omitempty"`
}

// YAMLServiceConfig represents a service configuration in the YAML recipe
type YAMLServiceConfig struct {
	// Remove indicates whether to remove this service
	Remove bool `yaml:"remove,omitempty"`

	// Image is the docker image to use
	Image string `yaml:"image,omitempty"`

	// Tag is the docker image tag
	Tag string `yaml:"tag,omitempty"`

	// Entrypoint overrides the container entrypoint
	Entrypoint string `yaml:"entrypoint,omitempty"`

	// Args are the arguments to pass to the service
	Args []string `yaml:"args,omitempty"`

	// Env is a map of environment variables
	Env map[string]string `yaml:"env,omitempty"`

	// Ports is a map of port name to port number
	Ports map[string]int `yaml:"ports,omitempty"`

	// Files is a map of container path to file source
	// File source can be:
	// - "artifact:<name>" to reference a runtime-generated artifact (e.g., "artifact:genesis.json")
	// - A relative path to a file in the same directory as the YAML recipe file
	Files map[string]string `yaml:"files,omitempty"`

	// Volumes is a map of container path to volume name
	Volumes map[string]string `yaml:"volumes,omitempty"`

	// DependsOn is a list of services this service depends on
	// Format: "service_name" or "service_name:condition" where condition is "healthy" or "running"
	DependsOn []string `yaml:"depends_on,omitempty"`

	// HostPath is the path to the binary on the host to run instead of Docker
	// When set, the service runs on the host machine instead of in a container
	HostPath string `yaml:"host_path,omitempty"`

	// Release specifies a GitHub release to download for host execution
	Release *YAMLReleaseConfig `yaml:"release,omitempty"`

	// ReadyCheck is a URL to check for service readiness (used for health checks)
	// Format: "http://localhost:PORT/path" - the service is ready when this URL returns 200
	ReadyCheck string `yaml:"ready_check,omitempty"`
}

// YAMLReleaseConfig specifies a GitHub release to download
type YAMLReleaseConfig struct {
	Name    string `yaml:"name"`
	Org     string `yaml:"org"`
	Repo    string `yaml:"repo,omitempty"`
	Version string `yaml:"version"`
	// Format specifies the download format: "tar.gz" (default) or "binary"
	// For "binary", downloads the raw binary directly without extraction
	Format string `yaml:"format,omitempty"`
}

// YAMLRecipe wraps a base recipe and applies YAML-based modifications
type YAMLRecipe struct {
	baseRecipe Recipe
	config     *YAMLRecipeConfig
	filePath   string
	recipeDir  string
}

var _ Recipe = &YAMLRecipe{}

// ParseYAMLRecipe parses a YAML recipe file and returns a YAMLRecipe
func ParseYAMLRecipe(filePath string, baseRecipes []Recipe) (*YAMLRecipe, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML recipe file: %w", err)
	}

	var config YAMLRecipeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML recipe: %w", err)
	}

	if config.Base == "" {
		return nil, fmt.Errorf("YAML recipe must specify a 'base' recipe (l1, opstack, or buildernet)")
	}

	// Find the base recipe
	var baseRecipe Recipe
	for _, r := range baseRecipes {
		if r.Name() == config.Base {
			baseRecipe = r
			break
		}
	}

	if baseRecipe == nil {
		return nil, fmt.Errorf("unknown base recipe: %s", config.Base)
	}

	return &YAMLRecipe{
		baseRecipe: baseRecipe,
		config:     &config,
		filePath:   filePath,
		recipeDir:  filepath.Dir(filePath),
	}, nil
}

func (y *YAMLRecipe) Name() string {
	return fmt.Sprintf("yaml(%s)", y.filePath)
}

func (y *YAMLRecipe) Description() string {
	return fmt.Sprintf("YAML recipe based on %s", y.config.Base)
}

func (y *YAMLRecipe) Flags() *flag.FlagSet {
	return y.baseRecipe.Flags()
}

func (y *YAMLRecipe) Artifacts() *ArtifactsBuilder {
	builder := y.baseRecipe.Artifacts()

	// Add extra files from the recipe directory
	for _, componentConfig := range y.config.Recipe {
		if componentConfig == nil || componentConfig.Services == nil {
			continue
		}
		for _, serviceConfig := range componentConfig.Services {
			if serviceConfig == nil || serviceConfig.Files == nil {
				continue
			}
			for _, fileSource := range serviceConfig.Files {
				// Skip artifact references (they're already in the artifacts)
				if strings.HasPrefix(fileSource, "artifact:") {
					continue
				}
				// This is a relative path - add it to the artifacts
				sourcePath := filepath.Join(y.recipeDir, fileSource)
				builder.WithExtraFile(fileSource, sourcePath)
			}
		}
	}

	return builder
}

func (y *YAMLRecipe) Apply(ctx *ExContext) *Component {
	// Get the base component tree
	baseComponent := y.baseRecipe.Apply(ctx)

	// Apply YAML modifications
	y.applyModifications(ctx, baseComponent)

	return baseComponent
}

func (y *YAMLRecipe) Output(manifest *Manifest) map[string]interface{} {
	return y.baseRecipe.Output(manifest)
}

// applyModifications applies the YAML recipe modifications to the component tree
func (y *YAMLRecipe) applyModifications(ctx *ExContext, component *Component) {
	if y.config.Recipe == nil {
		return
	}

	// Track removed services to clean up DependsOn references
	removedServices := make(map[string]bool)

	for componentName, componentConfig := range y.config.Recipe {
		if componentConfig == nil {
			continue
		}

		// Find or create the component
		existingComponent := findComponent(component, componentName)

		if componentConfig.Remove {
			// Remove the component and all its services
			if existingComponent != nil {
				collectServiceNames(existingComponent, removedServices)
				removeComponent(component, componentName)
			} else {
				slog.Warn("cannot remove component: not found", "name", componentName)
			}
			continue
		}

		// If component doesn't exist, create it
		if existingComponent == nil {
			existingComponent = NewComponent(componentName)
			component.Inner = append(component.Inner, existingComponent)
		}

		// Process services within the component
		if componentConfig.Services != nil {
			for serviceName, serviceConfig := range componentConfig.Services {
				if serviceConfig == nil {
					continue
				}

				// Find existing service
				existingService := findService(component, serviceName)

				if serviceConfig.Remove {
					// Remove the service
					if existingService != nil {
						removeService(component, serviceName)
						removedServices[serviceName] = true
					} else {
						slog.Warn("cannot remove service: not found", "name", serviceName, "component", componentName)
					}
					continue
				}

				if existingService != nil {
					// Override existing service properties
					applyServiceOverrides(existingService, serviceConfig, component)
				} else {
					// Create new service
					newService := createServiceFromConfig(serviceName, serviceConfig, component)
					existingComponent.Services = append(existingComponent.Services, newService)
				}
			}
		}
	}

	// Clean up DependsOn references to removed services
	if len(removedServices) > 0 {
		cleanupDependsOn(component, removedServices)
	}
}

// findComponent finds a component by name in the component tree
func findComponent(root *Component, name string) *Component {
	if root.Name == name {
		return root
	}

	for _, inner := range root.Inner {
		if found := findComponent(inner, name); found != nil {
			return found
		}
	}

	return nil
}

// removeComponent removes a component from the tree
func removeComponent(root *Component, name string) {
	for i, inner := range root.Inner {
		if inner.Name == name {
			root.Inner = append(root.Inner[:i], root.Inner[i+1:]...)
			return
		}
		removeComponent(inner, name)
	}
}

// findService finds a service by name in the component tree
func findService(root *Component, name string) *Service {
	for _, svc := range root.Services {
		if svc.Name == name {
			return svc
		}
	}

	for _, inner := range root.Inner {
		if found := findService(inner, name); found != nil {
			return found
		}
	}

	return nil
}

// removeService removes a service from the component tree
func removeService(root *Component, name string) {
	for i, svc := range root.Services {
		if svc.Name == name {
			root.Services = append(root.Services[:i], root.Services[i+1:]...)
			return
		}
	}

	for _, inner := range root.Inner {
		removeService(inner, name)
	}
}

// collectServiceNames collects all service names from a component and its children
func collectServiceNames(component *Component, names map[string]bool) {
	for _, svc := range component.Services {
		names[svc.Name] = true
	}
	for _, inner := range component.Inner {
		collectServiceNames(inner, names)
	}
}

// cleanupDependsOn removes DependsOn, NodeRefs, and Args references to removed services
func cleanupDependsOn(root *Component, removedServices map[string]bool) {
	for _, svc := range root.Services {
		// Filter out removed services from DependsOn
		if svc.DependsOn != nil {
			filtered := make([]*DependsOn, 0, len(svc.DependsOn))
			for _, dep := range svc.DependsOn {
				if !removedServices[dep.Name] {
					filtered = append(filtered, dep)
				}
			}
			svc.DependsOn = filtered
		}

		// Filter out removed services from NodeRefs
		if svc.NodeRefs != nil {
			filteredRefs := make([]*NodeRef, 0, len(svc.NodeRefs))
			for _, ref := range svc.NodeRefs {
				if !removedServices[ref.Service] {
					filteredRefs = append(filteredRefs, ref)
				}
			}
			svc.NodeRefs = filteredRefs
		}

		// Filter out Args that reference removed services
		if svc.Args != nil {
			filteredArgs := make([]string, 0, len(svc.Args))
			skipNext := false
			for i, arg := range svc.Args {
				if skipNext {
					skipNext = false
					continue
				}
				// Check if this arg contains a Service template reference to a removed service
				if containsRemovedServiceRef(arg, removedServices) {
					// Also skip the previous arg if it looks like a flag (starts with -)
					if len(filteredArgs) > 0 && len(filteredArgs[len(filteredArgs)-1]) > 0 && filteredArgs[len(filteredArgs)-1][0] == '-' {
						filteredArgs = filteredArgs[:len(filteredArgs)-1]
					}
					continue
				}
				// Check if this is a flag and the next arg references a removed service
				if len(arg) > 0 && arg[0] == '-' && i+1 < len(svc.Args) {
					if containsRemovedServiceRef(svc.Args[i+1], removedServices) {
						skipNext = true
						continue
					}
				}
				filteredArgs = append(filteredArgs, arg)
			}
			svc.Args = filteredArgs
		}
	}

	for _, inner := range root.Inner {
		cleanupDependsOn(inner, removedServices)
	}
}

// containsRemovedServiceRef checks if an arg string contains a Service template reference to a removed service
func containsRemovedServiceRef(arg string, removedServices map[string]bool) bool {
	for serviceName := range removedServices {
		// Check for template pattern like {{Service "serviceName" ...}}
		if strings.Contains(arg, `{{Service "`+serviceName+`"`) {
			return true
		}
	}
	return false
}

// applyServiceOverrides applies YAML config overrides to an existing service
func applyServiceOverrides(svc *Service, config *YAMLServiceConfig, root *Component) {
	if config.Image != "" {
		svc.Image = config.Image
	}
	if config.Tag != "" {
		svc.Tag = config.Tag
	}
	if config.Entrypoint != "" {
		svc.Entrypoint = config.Entrypoint
	}
	if len(config.Args) > 0 {
		svc.Args = config.Args
	}
	if config.Env != nil {
		if svc.Env == nil {
			svc.Env = make(map[string]string)
		}
		for k, v := range config.Env {
			svc.Env[k] = v
		}
	}
	if config.Ports != nil {
		for portName, portNum := range config.Ports {
			svc.WithPort(portName, portNum)
		}
	}
	if config.Files != nil {
		applyFilesToService(svc, config.Files)
	}
	if config.Volumes != nil {
		for containerPath, volumeName := range config.Volumes {
			svc.WithVolume(volumeName, containerPath)
		}
	}
	if config.DependsOn != nil {
		applyDependsOn(svc, config.DependsOn, root)
	}
	if config.HostPath != "" {
		svc.HostPath = config.HostPath
		svc.UseHostExecution()
	}
	if config.Release != nil {
		rel := yamlReleaseToRelease(config.Release)
		svc.WithRelease(rel)
		svc.UseHostExecution()
	}
	if config.ReadyCheck != "" {
		svc.WithReady(ReadyCheck{QueryURL: config.ReadyCheck})
	}
}

// yamlReleaseToRelease converts a YAMLReleaseConfig to a release struct
func yamlReleaseToRelease(cfg *YAMLReleaseConfig) *release {
	return &release{
		Name:    cfg.Name,
		Org:     cfg.Org,
		Repo:    cfg.Repo,
		Version: cfg.Version,
		Format:  cfg.Format,
		Arch: func(goos, goarch string) string {
			// For binary format, arch is not used
			if cfg.Format == "binary" {
				return ""
			}
			// Default architecture mapping for tar.gz
			if goos == "linux" {
				return "x86_64-unknown-linux-gnu"
			} else if goos == "darwin" && goarch == "arm64" {
				return "aarch64-apple-darwin"
			} else if goos == "darwin" && goarch == "amd64" {
				return "x86_64-apple-darwin"
			}
			return ""
		},
	}
}

// applyFilesToService maps files to a service
func applyFilesToService(svc *Service, files map[string]string) {
	for containerPath, fileSource := range files {
		var artifactName string
		if strings.HasPrefix(fileSource, "artifact:") {
			artifactName = strings.TrimPrefix(fileSource, "artifact:")
		} else {
			artifactName = fileSource
		}
		svc.WithArtifact(containerPath, artifactName)
	}
}

// applyDependsOn parses depends_on entries and adds them to the service
// Supports formats: "service", "service:condition", "component.service", "component.service:condition"
// The root parameter is used to validate component names when using the component.service format.
func applyDependsOn(svc *Service, dependsOn []string, root *Component) {
	for _, dep := range dependsOn {
		parts := strings.SplitN(dep, ":", 2)
		serviceName := parts[0]
		condition := "healthy"
		if len(parts) == 2 {
			condition = parts[1]
		}

		// Handle component.service format - extract just the service name
		if strings.Contains(serviceName, ".") {
			componentParts := strings.SplitN(serviceName, ".", 2)
			if len(componentParts) == 2 {
				componentName := componentParts[0]
				serviceName = componentParts[1]

				// Validate that the component exists
				if root != nil && findComponent(root, componentName) == nil {
					slog.Warn("depends_on references unknown component",
						"component", componentName,
						"service", serviceName,
						"full_reference", dep)
				}
			}
		}

		switch condition {
		case "healthy":
			svc.DependsOnHealthy(serviceName)
		case "running":
			svc.DependsOnRunning(serviceName)
		default:
			svc.DependsOnHealthy(serviceName)
		}
	}
}

// createServiceFromConfig creates a new service from YAML config
func createServiceFromConfig(name string, config *YAMLServiceConfig, root *Component) *Service {
	svc := &Service{
		Name:     name,
		Args:     []string{},
		Ports:    []*Port{},
		NodeRefs: []*NodeRef{},
	}

	if config.Image != "" {
		svc.Image = config.Image
	}
	if config.Tag != "" {
		svc.Tag = config.Tag
	}
	if config.Entrypoint != "" {
		svc.Entrypoint = config.Entrypoint
	}
	if len(config.Args) > 0 {
		svc.Args = config.Args
	}
	if config.Env != nil {
		svc.Env = make(map[string]string)
		for k, v := range config.Env {
			svc.Env[k] = v
		}
	}
	if config.Ports != nil {
		for portName, portNum := range config.Ports {
			svc.WithPort(portName, portNum)
		}
	}
	if config.Files != nil {
		applyFilesToService(svc, config.Files)
	}
	if config.Volumes != nil {
		for containerPath, volumeName := range config.Volumes {
			svc.WithVolume(volumeName, containerPath)
		}
	}
	if config.DependsOn != nil {
		applyDependsOn(svc, config.DependsOn, root)
	}
	if config.HostPath != "" {
		svc.HostPath = config.HostPath
		svc.UseHostExecution()
	}
	if config.Release != nil {
		rel := yamlReleaseToRelease(config.Release)
		svc.WithRelease(rel)
		svc.UseHostExecution()
	}
	if config.ReadyCheck != "" {
		svc.WithReady(ReadyCheck{QueryURL: config.ReadyCheck})
	}

	return svc
}

// IsYAMLRecipeFile checks if the given path looks like a YAML recipe file
func IsYAMLRecipeFile(path string) bool {
	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}

	// Check for .yaml or .yml extension
	return strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")
}
