package playground

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

func TestConvertServiceToYAML(t *testing.T) {
	tests := []struct {
		name     string
		service  *Service
		expected *YAMLServiceConfig
	}{
		{
			name: "basic service",
			service: &Service{
				Name:       "test-service",
				Image:      "test-image",
				Tag:        "v1.0.0",
				Entrypoint: "/bin/sh",
				Args:       []string{"-c", "echo hello"},
			},
			expected: &YAMLServiceConfig{
				Image:      "test-image",
				Tag:        "v1.0.0",
				Entrypoint: "/bin/sh",
				Args:       []string{"-c", "echo hello"},
			},
		},
		{
			name: "service with ports",
			service: &Service{
				Name:  "web-service",
				Image: "nginx",
				Ports: []*Port{
					{Name: "http", Port: 8080},
					{Name: "https", Port: 8443},
				},
			},
			expected: &YAMLServiceConfig{
				Image: "nginx",
				Ports: map[string]int{
					"http":  8080,
					"https": 8443,
				},
			},
		},
		{
			name: "service with env vars",
			service: &Service{
				Name:  "app",
				Image: "myapp",
				Env: map[string]string{
					"DEBUG": "true",
					"PORT":  "3000",
				},
			},
			expected: &YAMLServiceConfig{
				Image: "myapp",
				Env: map[string]string{
					"DEBUG": "true",
					"PORT":  "3000",
				},
			},
		},
		{
			name: "service with files",
			service: &Service{
				Name:  "config-service",
				Image: "app",
				FilesMapped: map[string]string{
					"/app/config.json": "config.json",
				},
			},
			expected: &YAMLServiceConfig{
				Image: "app",
				Files: map[string]string{
					"/app/config.json": "artifact:config.json",
				},
			},
		},
		{
			name: "service with volumes",
			service: &Service{
				Name:  "data-service",
				Image: "postgres",
				VolumesMapped: map[string]*VolumeMapped{
					"/var/lib/postgresql/data": {Name: "pgdata"},
				},
			},
			expected: &YAMLServiceConfig{
				Image: "postgres",
				Volumes: map[string]*YAMLVolumeMappedConfig{
					"/var/lib/postgresql/data": {Name: "pgdata"},
				},
			},
		},
		{
			name: "service with depends_on healthy",
			service: &Service{
				Name:  "app",
				Image: "myapp",
				DependsOn: []*DependsOn{
					{Name: "db", Condition: DependsOnConditionHealthy},
				},
			},
			expected: &YAMLServiceConfig{
				Image:     "myapp",
				DependsOn: []string{"db:healthy"},
			},
		},
		{
			name: "service with depends_on running",
			service: &Service{
				Name:  "app",
				Image: "myapp",
				DependsOn: []*DependsOn{
					{Name: "cache", Condition: DependsOnConditionRunning},
				},
			},
			expected: &YAMLServiceConfig{
				Image:     "myapp",
				DependsOn: []string{"cache:running"},
			},
		},
		{
			name: "service with host_path",
			service: &Service{
				Name:     "local-binary",
				HostPath: "/usr/local/bin/myapp",
			},
			expected: &YAMLServiceConfig{
				HostPath: "/usr/local/bin/myapp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertServiceToYAML(tt.service)

			require.Equal(t, tt.expected.Image, result.Image)
			require.Equal(t, tt.expected.Tag, result.Tag)
			require.Equal(t, tt.expected.Entrypoint, result.Entrypoint)
			require.Equal(t, tt.expected.HostPath, result.HostPath)
			require.Len(t, result.Args, len(tt.expected.Args))
			require.Equal(t, tt.expected.Ports, result.Ports)
			require.Equal(t, tt.expected.Env, result.Env)
			require.Equal(t, tt.expected.Files, result.Files)
			require.Equal(t, tt.expected.Volumes, result.Volumes)
			require.Len(t, result.DependsOn, len(tt.expected.DependsOn))
		})
	}
}

func TestConvertServiceToYAML_MultilineArgs(t *testing.T) {
	service := &Service{
		Name:  "shell-service",
		Image: "alpine",
		Args:  []string{"-c", "echo hello && echo world && echo done"},
	}

	result := convertServiceToYAML(service)

	require.Len(t, result.Args, 2)
	require.Contains(t, result.Args[1], "\\\n")
}

func TestConvertServiceToYAML_LongArgs(t *testing.T) {
	longArg := strings.Repeat("x", 101) + " --flag1 value1 --flag2 value2"
	service := &Service{
		Name:  "long-cmd",
		Image: "alpine",
		Args:  []string{longArg},
	}

	result := convertServiceToYAML(service)

	require.Len(t, result.Args, 1)
	require.Contains(t, result.Args[0], "\\\n--")
}

func TestConvertComponentToYAML(t *testing.T) {
	component := &Component{
		Name: "test-component",
		Services: []*Service{
			{Name: "svc1", Image: "image1", Tag: "v1"},
			{Name: "svc2", Image: "image2", Tag: "v2"},
		},
	}

	recipe := make(map[string]*YAMLComponentConfig)
	convertComponentToYAML(component, recipe)

	require.Len(t, recipe, 1)
	require.Contains(t, recipe, "test-component")
	require.Len(t, recipe["test-component"].Services, 2)
	require.Equal(t, "image1", recipe["test-component"].Services["svc1"].Image)
	require.Equal(t, "image2", recipe["test-component"].Services["svc2"].Image)
}

func TestConvertComponentToYAML_Nested(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{
				Name:     "child1",
				Services: []*Service{{Name: "child1-svc", Image: "child1-image"}},
			},
			{
				Name:     "child2",
				Services: []*Service{{Name: "child2-svc", Image: "child2-image"}},
			},
		},
	}

	recipe := make(map[string]*YAMLComponentConfig)
	convertComponentToYAML(root, recipe)

	require.Len(t, recipe, 2)
	require.Contains(t, recipe, "child1")
	require.Contains(t, recipe, "child2")
}

func TestConvertComponentToYAML_NilComponent(t *testing.T) {
	recipe := make(map[string]*YAMLComponentConfig)
	convertComponentToYAML(nil, recipe)

	require.Empty(t, recipe)
}

func TestConvertComponentToYAML_EmptyServices(t *testing.T) {
	component := &Component{
		Name:     "empty-component",
		Services: []*Service{},
	}

	recipe := make(map[string]*YAMLComponentConfig)
	convertComponentToYAML(component, recipe)

	require.Empty(t, recipe)
}

func TestRecipeToYAML(t *testing.T) {
	t.Run("L1Recipe", func(t *testing.T) {
		recipe := &L1Recipe{}

		yamlStr, err := RecipeToYAML(recipe)

		require.NoError(t, err)
		require.NotEmpty(t, yamlStr)
		require.Contains(t, yamlStr, "base: l1")
		require.Contains(t, yamlStr, "reth:")
		require.Contains(t, yamlStr, "lighthouse-beacon-node:")
		require.Contains(t, yamlStr, "lighthouse-validator-node:")
		require.Contains(t, yamlStr, "mev-boost-relay:")
	})

	t.Run("OpRecipe", func(t *testing.T) {
		recipe := &OpRecipe{}

		yamlStr, err := RecipeToYAML(recipe)

		require.NoError(t, err)
		require.NotEmpty(t, yamlStr)
		require.Contains(t, yamlStr, "base: opstack")
		require.Contains(t, yamlStr, "op-node:")
		require.Contains(t, yamlStr, "op-geth:")
	})

	t.Run("BuilderNetRecipe", func(t *testing.T) {
		recipe := &BuilderNetRecipe{}

		yamlStr, err := RecipeToYAML(recipe)

		require.NoError(t, err)
		require.NotEmpty(t, yamlStr)
		require.Contains(t, yamlStr, "base: buildernet")
	})

	t.Run("contains service details", func(t *testing.T) {
		recipe := &L1Recipe{}

		yamlStr, err := RecipeToYAML(recipe)

		require.NoError(t, err)
		require.Contains(t, yamlStr, "services:")
		require.Contains(t, yamlStr, "image:")
		require.Contains(t, yamlStr, "tag:")
	})

	t.Run("valid YAML structure", func(t *testing.T) {
		recipe := &L1Recipe{}

		yamlStr, err := RecipeToYAML(recipe)

		require.NoError(t, err)

		// Parse the output to verify it's valid YAML
		var config YAMLRecipeConfig
		err = yaml.Unmarshal([]byte(yamlStr), &config)
		require.NoError(t, err)
		require.Equal(t, "l1", config.Base)
		require.NotEmpty(t, config.Recipe)
	})
}
