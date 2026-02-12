package playground

import (
	"fmt"
	"os"
	"strings"
)

// ValidationResult holds the results of recipe validation
type ValidationResult struct {
	Errors   []string
	Warnings []string
}

func (v *ValidationResult) AddError(format string, args ...interface{}) {
	v.Errors = append(v.Errors, fmt.Sprintf(format, args...))
}

func (v *ValidationResult) AddWarning(format string, args ...interface{}) {
	v.Warnings = append(v.Warnings, fmt.Sprintf(format, args...))
}

func (v *ValidationResult) IsValid() bool {
	return len(v.Errors) == 0
}

// ValidateRecipe validates a recipe without starting it
func ValidateRecipe(recipe Recipe, baseRecipes []Recipe) *ValidationResult {
	result := &ValidationResult{}

	// For YAML recipes, do additional validation
	if yamlRecipe, ok := recipe.(*YAMLRecipe); ok {
		validateYAMLRecipe(yamlRecipe, baseRecipes, result)
	}

	// Build a minimal manifest to validate structure
	exCtx := &ExContext{
		LogLevel: LevelInfo,
		Contender: &ContenderContext{
			Enabled: false,
		},
	}

	component := recipe.Apply(exCtx)
	manifest := NewManifest("validation-test", component)

	// Validate service names are unique
	validateUniqueServiceNames(manifest, result)

	// Validate port configurations
	validatePorts(manifest, result)

	// Validate dependencies
	validateDependencies(manifest, result)

	// Validate host paths exist (if specified)
	validateHostPaths(manifest, result)

	return result
}

func validateYAMLRecipe(recipe *YAMLRecipe, baseRecipes []Recipe, result *ValidationResult) {
	// Check base recipe exists
	baseFound := false
	for _, r := range baseRecipes {
		if r.Name() == recipe.config.Base {
			baseFound = true
			break
		}
	}
	if !baseFound {
		result.AddError("base recipe '%s' not found", recipe.config.Base)
	}

	// Check for potential component/service name mismatches
	if recipe.config.Recipe != nil {
		for componentName, componentConfig := range recipe.config.Recipe {
			if componentConfig == nil {
				continue
			}

			// Warn about common naming issues
			if componentConfig.Remove {
				// Check if trying to remove a component that might not exist
				if strings.Contains(componentName, "-") {
					result.AddWarning("removing component '%s' - verify this matches the base recipe component name", componentName)
				}
			}

			if componentConfig.Services != nil {
				for serviceName, serviceConfig := range componentConfig.Services {
					if serviceConfig == nil {
						continue
					}
					if serviceConfig.Remove {
						result.AddWarning("removing service '%s' from component '%s' - verify names match base recipe", serviceName, componentName)
					}

					// Validate lifecycle cannot be used with host_path, release, or args
					validateLifecycleConfig(serviceName, componentName, serviceConfig, result)

					// Validate args and replace_args are mutually exclusive
					if len(serviceConfig.Args) > 0 && len(serviceConfig.ReplaceArgs) > 0 {
						result.AddError("service '%s' in component '%s': args and replace_args cannot be used together", serviceName, componentName)
					}
				}
			}
		}
	}
}

// validateLifecycleConfig checks that lifecycle_hooks is not used with incompatible options
// and that init/start/stop are only used when lifecycle_hooks is true
func validateLifecycleConfig(serviceName, componentName string, config *YAMLServiceConfig, result *ValidationResult) {
	hasLifecycleFields := len(config.Init) > 0 || config.Start != "" || len(config.Stop) > 0

	// If lifecycle_hooks is not set but lifecycle fields are used, that's an error
	if !config.LifecycleHooks {
		if hasLifecycleFields {
			result.AddError("service '%s' in component '%s': init, start, and stop require lifecycle_hooks: true", serviceName, componentName)
		}
		return
	}

	// lifecycle_hooks is true - check for incompatible options
	if config.HostPath != "" {
		result.AddError("service '%s' in component '%s': lifecycle_hooks cannot be used with host_path", serviceName, componentName)
	}
	if config.Release != nil {
		result.AddError("service '%s' in component '%s': lifecycle_hooks cannot be used with release", serviceName, componentName)
	}
	if len(config.Args) > 0 {
		result.AddError("service '%s' in component '%s': lifecycle_hooks cannot be used with args", serviceName, componentName)
	}

	// Validate that at least one of init or start is specified
	if len(config.Init) == 0 && config.Start == "" {
		result.AddError("service '%s' in component '%s': lifecycle_hooks requires at least one of init or start", serviceName, componentName)
	}
}

func validateUniqueServiceNames(manifest *Manifest, result *ValidationResult) {
	seen := make(map[string]bool)
	for _, svc := range manifest.Services {
		if seen[svc.Name] {
			result.AddError("duplicate service name: %s", svc.Name)
		}
		seen[svc.Name] = true
	}
}

func validatePorts(manifest *Manifest, result *ValidationResult) {
	// Check for port number conflicts across services
	portUsage := make(map[int][]string) // port number -> service names

	for _, svc := range manifest.Services {
		for _, port := range svc.Ports {
			portUsage[port.Port] = append(portUsage[port.Port], svc.Name)
		}
	}

	for portNum, services := range portUsage {
		if len(services) > 1 {
			result.AddWarning("port %d used by multiple services: %s (may conflict if running on same host)", portNum, strings.Join(services, ", "))
		}
	}
}

func validateDependencies(manifest *Manifest, result *ValidationResult) {
	serviceNames := make(map[string]bool)
	for _, svc := range manifest.Services {
		serviceNames[svc.Name] = true
	}

	for _, svc := range manifest.Services {
		for _, dep := range svc.DependsOn {
			depName := dep.Name

			// Handle healthmon sidecars
			depName = strings.TrimSuffix(depName, "_readycheck")

			// Handle component.service format (e.g., "merger.merger-builder")
			if strings.Contains(depName, ".") {
				parts := strings.SplitN(depName, ".", 2)
				if len(parts) == 2 {
					depName = parts[1] // Use just the service name
				}
			}

			if !serviceNames[depName] && !serviceNames[dep.Name] {
				result.AddError("service '%s' depends on unknown service '%s'", svc.Name, dep.Name)
			}
		}

		// Check NodeRefs
		for _, ref := range svc.NodeRefs {
			if !serviceNames[ref.Service] {
				result.AddError("service '%s' references unknown service '%s'", svc.Name, ref.Service)
			}
		}
	}
}

func validateHostPaths(manifest *Manifest, result *ValidationResult) {
	for _, svc := range manifest.Services {
		if svc.HostPath != "" {
			if _, err := os.Stat(svc.HostPath); os.IsNotExist(err) {
				result.AddError("service '%s' host_path does not exist: %s", svc.Name, svc.HostPath)
			}
		}
	}
}
