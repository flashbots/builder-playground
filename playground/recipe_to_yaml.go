package playground

import (
	"fmt"
	"math/big"
	"strings"

	"gopkg.in/yaml.v3"
)

// RecipeToYAML converts a recipe to a playground.yaml format
func RecipeToYAML(recipe Recipe) (string, error) {
	// Create a minimal output for the context (needed by some components)
	out := &output{
		dst:          "/tmp/playground-generate",
		enodeAddrSeq: big.NewInt(0),
	}

	// Create an execution context to apply the recipe
	ctx := &ExContext{
		LogLevel: LevelInfo,
		Contender: &ContenderContext{
			Enabled: false,
		},
		Output: out,
	}

	// Apply the recipe to get the component tree
	component := recipe.Apply(ctx)

	// Build the YAML structure
	yamlConfig := &YAMLRecipeConfig{
		Base:   recipe.Name(),
		Recipe: make(map[string]*YAMLComponentConfig),
	}

	// Convert component tree to YAML config
	convertComponentToYAML(component, yamlConfig.Recipe)

	// Marshal to YAML
	var sb strings.Builder
	encoder := yaml.NewEncoder(&sb)
	encoder.SetIndent(2)
	if err := encoder.Encode(yamlConfig); err != nil {
		return "", fmt.Errorf("failed to encode YAML: %w", err)
	}

	return sb.String(), nil
}

func convertComponentToYAML(component *Component, recipe map[string]*YAMLComponentConfig) {
	if component == nil {
		return
	}

	// Process services in this component
	if len(component.Services) > 0 {
		componentConfig := &YAMLComponentConfig{
			Services: make(map[string]*YAMLServiceConfig),
		}

		for _, svc := range component.Services {
			svcConfig := convertServiceToYAML(svc)
			componentConfig.Services[svc.Name] = svcConfig
		}

		recipe[component.Name] = componentConfig
	}

	// Process inner components
	for _, inner := range component.Inner {
		convertComponentToYAML(inner, recipe)
	}
}

func convertServiceToYAML(svc *Service) *YAMLServiceConfig {
	config := &YAMLServiceConfig{}

	if svc.Image != "" {
		config.Image = svc.Image
	}
	if svc.Tag != "" {
		config.Tag = svc.Tag
	}
	if svc.Entrypoint != "" {
		config.Entrypoint = svc.Entrypoint
	}
	if len(svc.Args) > 0 {
		// For shell commands, format as multiline for readability
		args := make([]string, len(svc.Args))
		copy(args, svc.Args)
		for i, arg := range args {
			if strings.Contains(arg, " && ") || (len(arg) > 100 && strings.Contains(arg, " --")) {
				// Replace " && " with newline
				arg = strings.ReplaceAll(arg, " && ", " && \\\n")
				// Replace " --" with newline for long commands
				arg = strings.ReplaceAll(arg, " --", " \\\n--")
				args[i] = arg
			}
		}
		config.Args = args
	}
	if len(svc.Env) > 0 {
		config.Env = svc.Env
	}
	if len(svc.Ports) > 0 {
		config.Ports = make(map[string]int)
		for _, p := range svc.Ports {
			config.Ports[p.Name] = p.Port
		}
	}
	if len(svc.FilesMapped) > 0 {
		config.Files = make(map[string]string)
		for containerPath, artifactName := range svc.FilesMapped {
			config.Files[containerPath] = "artifact:" + artifactName
		}
	}
	if len(svc.VolumesMapped) > 0 {
		config.Volumes = make(map[string]string)
		for containerPath, volumeName := range svc.VolumesMapped {
			config.Volumes[containerPath] = volumeName
		}
	}
	if len(svc.DependsOn) > 0 {
		config.DependsOn = make([]string, 0, len(svc.DependsOn))
		for _, dep := range svc.DependsOn {
			depStr := dep.Name
			if dep.Condition == DependsOnConditionHealthy {
				depStr += ":healthy"
			} else if dep.Condition == DependsOnConditionRunning {
				depStr += ":running"
			}
			config.DependsOn = append(config.DependsOn, depStr)
		}
	}
	if svc.HostPath != "" {
		config.HostPath = svc.HostPath
	}

	return config
}
