package playground

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateLifecycleConfig(t *testing.T) {
	tests := []struct {
		name           string
		config         *YAMLServiceConfig
		expectedErrors int
		errorContains  []string
	}{
		{
			name: "valid lifecycle config with init start and stop",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Init:           []string{"echo init"},
				Start:          "./start.sh",
				Stop:           []string{"echo stop"},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with only init",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Init:           []string{"echo init"},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with init and stop",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Init:           []string{"echo init"},
				Stop:           []string{"echo stop"},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with only start",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Start:          "./start.sh",
			},
			expectedErrors: 0,
		},
		{
			name: "lifecycle_hooks with host_path",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				HostPath:       "/usr/bin/app",
				Start:          "./start.sh",
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle_hooks cannot be used with host_path"},
		},
		{
			name: "lifecycle_hooks with release",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Release: &YAMLReleaseConfig{
					Name:    "app",
					Org:     "org",
					Version: "v1.0.0",
				},
				Start: "./start.sh",
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle_hooks cannot be used with release"},
		},
		{
			name: "lifecycle_hooks with args",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Args:           []string{"--port", "8080"},
				Start:          "./start.sh",
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle_hooks cannot be used with args"},
		},
		{
			name: "lifecycle_hooks with only stop - invalid",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				Stop:           []string{"echo stop"},
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle_hooks requires at least one of init or start"},
		},
		{
			name: "lifecycle_hooks with nothing - invalid",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle_hooks requires at least one of init or start"},
		},
		{
			name: "lifecycle_hooks with all incompatible options",
			config: &YAMLServiceConfig{
				LifecycleHooks: true,
				HostPath:       "/usr/bin/app",
				Release: &YAMLReleaseConfig{
					Name:    "app",
					Org:     "org",
					Version: "v1.0.0",
				},
				Args:  []string{"--port", "8080"},
				Start: "./start.sh",
			},
			expectedErrors: 3,
			errorContains: []string{
				"lifecycle_hooks cannot be used with host_path",
				"lifecycle_hooks cannot be used with release",
				"lifecycle_hooks cannot be used with args",
			},
		},
		{
			name: "no lifecycle_hooks - no errors",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Args:     []string{"--port", "8080"},
			},
			expectedErrors: 0,
		},
		{
			name: "init without lifecycle_hooks - invalid",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Init:     []string{"echo init"},
			},
			expectedErrors: 1,
			errorContains:  []string{"init, start, and stop require lifecycle_hooks: true"},
		},
		{
			name: "start without lifecycle_hooks - invalid",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Start:    "./start.sh",
			},
			expectedErrors: 1,
			errorContains:  []string{"init, start, and stop require lifecycle_hooks: true"},
		},
		{
			name: "stop without lifecycle_hooks - invalid",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Stop:     []string{"echo stop"},
			},
			expectedErrors: 1,
			errorContains:  []string{"init, start, and stop require lifecycle_hooks: true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{}
			validateLifecycleConfig("test-svc", "test-component", tt.config, result)

			require.Len(t, result.Errors, tt.expectedErrors)
			for _, expected := range tt.errorContains {
				found := false
				for _, err := range result.Errors {
					if strings.Contains(err, expected) {
						found = true
						break
					}
				}
				require.True(t, found, "expected error containing '%s' not found in %v", expected, result.Errors)
			}
		})
	}
}

func TestValidateLifecycleConfig_InYAMLRecipe(t *testing.T) {
	// Test that lifecycle validation is called during YAML recipe validation
	recipe := &YAMLRecipe{
		config: &YAMLRecipeConfig{
			Base: "l1",
			Recipe: map[string]*YAMLComponentConfig{
				"test-component": {
					Services: map[string]*YAMLServiceConfig{
						"test-svc": {
							LifecycleHooks: true,
							HostPath:       "/usr/bin/app",
							Start:          "./start.sh",
						},
					},
				},
			},
		},
	}

	baseRecipes := []Recipe{&L1Recipe{}}
	result := &ValidationResult{}
	validateYAMLRecipe(recipe, baseRecipes, result)

	require.NotEmpty(t, result.Errors)
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "lifecycle_hooks cannot be used with host_path") {
			found = true
			break
		}
	}
	require.True(t, found, "expected lifecycle validation error not found")
}

func TestValidateLifecycleConfig_InYAMLRecipe_WithoutLifecycleHooks(t *testing.T) {
	// Test that init/start/stop without lifecycle_hooks is caught during YAML recipe validation
	recipe := &YAMLRecipe{
		config: &YAMLRecipeConfig{
			Base: "l1",
			Recipe: map[string]*YAMLComponentConfig{
				"test-component": {
					Services: map[string]*YAMLServiceConfig{
						"test-svc": {
							HostPath: "/usr/bin/app",
							Start:    "./start.sh",
						},
					},
				},
			},
		},
	}

	baseRecipes := []Recipe{&L1Recipe{}}
	result := &ValidationResult{}
	validateYAMLRecipe(recipe, baseRecipes, result)

	require.NotEmpty(t, result.Errors)
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "init, start, and stop require lifecycle_hooks: true") {
			found = true
			break
		}
	}
	require.True(t, found, "expected lifecycle validation error not found in %v", result.Errors)
}
