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
				Lifecycle: &YAMLLifecycleConfig{
					Init:  []string{"echo init"},
					Start: "./start.sh",
					Stop:  []string{"echo stop"},
				},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with only init",
			config: &YAMLServiceConfig{
				Lifecycle: &YAMLLifecycleConfig{
					Init: []string{"echo init"},
				},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with init and stop",
			config: &YAMLServiceConfig{
				Lifecycle: &YAMLLifecycleConfig{
					Init: []string{"echo init"},
					Stop: []string{"echo stop"},
				},
			},
			expectedErrors: 0,
		},
		{
			name: "valid lifecycle config with only start",
			config: &YAMLServiceConfig{
				Lifecycle: &YAMLLifecycleConfig{
					Start: "./start.sh",
				},
			},
			expectedErrors: 0,
		},
		{
			name: "lifecycle with host_path",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Lifecycle: &YAMLLifecycleConfig{
					Start: "./start.sh",
				},
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle cannot be used with host_path"},
		},
		{
			name: "lifecycle with release",
			config: &YAMLServiceConfig{
				Release: &YAMLReleaseConfig{
					Name:    "app",
					Org:     "org",
					Version: "v1.0.0",
				},
				Lifecycle: &YAMLLifecycleConfig{
					Start: "./start.sh",
				},
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle cannot be used with release"},
		},
		{
			name: "lifecycle with args",
			config: &YAMLServiceConfig{
				Args: []string{"--port", "8080"},
				Lifecycle: &YAMLLifecycleConfig{
					Start: "./start.sh",
				},
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle cannot be used with args"},
		},
		{
			name: "lifecycle with only stop - invalid",
			config: &YAMLServiceConfig{
				Lifecycle: &YAMLLifecycleConfig{
					Stop: []string{"echo stop"},
				},
			},
			expectedErrors: 2,
			errorContains: []string{
				"lifecycle requires at least one of init or start",
				"lifecycle.stop cannot be specified without init or start",
			},
		},
		{
			name: "lifecycle with nothing - invalid",
			config: &YAMLServiceConfig{
				Lifecycle: &YAMLLifecycleConfig{},
			},
			expectedErrors: 1,
			errorContains:  []string{"lifecycle requires at least one of init or start"},
		},
		{
			name: "lifecycle with all incompatible options",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Release: &YAMLReleaseConfig{
					Name:    "app",
					Org:     "org",
					Version: "v1.0.0",
				},
				Args: []string{"--port", "8080"},
				Lifecycle: &YAMLLifecycleConfig{
					Start: "./start.sh",
				},
			},
			expectedErrors: 3,
			errorContains: []string{
				"lifecycle cannot be used with host_path",
				"lifecycle cannot be used with release",
				"lifecycle cannot be used with args",
			},
		},
		{
			name: "no lifecycle - no errors",
			config: &YAMLServiceConfig{
				HostPath: "/usr/bin/app",
				Args:     []string{"--port", "8080"},
			},
			expectedErrors: 0,
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
							HostPath: "/usr/bin/app",
							Lifecycle: &YAMLLifecycleConfig{
								Start: "./start.sh",
							},
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
		if strings.Contains(err, "lifecycle cannot be used with host_path") {
			found = true
			break
		}
	}
	require.True(t, found, "expected lifecycle validation error not found")
}
