package playground

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindComponent(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{
				Name:  "child1",
				Inner: []*Component{{Name: "grandchild1"}},
			},
			{Name: "child2"},
		},
	}

	tests := []struct {
		name     string
		search   string
		expected bool
	}{
		{"find root", "root", true},
		{"find child1", "child1", true},
		{"find child2", "child2", true},
		{"find grandchild1", "grandchild1", true},
		{"not found", "nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findComponent(root, tt.search)
			if tt.expected {
				require.NotNil(t, result)
				require.Equal(t, tt.search, result.Name)
			} else {
				require.Nil(t, result)
			}
		})
	}
}

func TestRemoveComponent(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{Name: "child1"},
			{Name: "child2"},
			{Name: "child3"},
		},
	}

	removeComponent(root, "child2")

	require.Len(t, root.Inner, 2)
	for _, c := range root.Inner {
		require.NotEqual(t, "child2", c.Name)
	}
}

func TestFindService(t *testing.T) {
	root := &Component{
		Name:     "root",
		Services: []*Service{{Name: "root-svc"}},
		Inner: []*Component{
			{
				Name:     "child",
				Services: []*Service{{Name: "child-svc"}},
			},
		},
	}

	tests := []struct {
		name     string
		search   string
		expected bool
	}{
		{"find root service", "root-svc", true},
		{"find child service", "child-svc", true},
		{"not found", "nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findService(root, tt.search)
			if tt.expected {
				require.NotNil(t, result)
			} else {
				require.Nil(t, result)
			}
		})
	}
}

func TestRemoveService(t *testing.T) {
	root := &Component{
		Name: "root",
		Services: []*Service{
			{Name: "svc1"},
			{Name: "svc2"},
			{Name: "svc3"},
		},
	}

	removeService(root, "svc2")

	require.Len(t, root.Services, 2)
	for _, s := range root.Services {
		require.NotEqual(t, "svc2", s.Name)
	}
}

func TestCollectServiceNames(t *testing.T) {
	root := &Component{
		Name:     "root",
		Services: []*Service{{Name: "svc1"}, {Name: "svc2"}},
		Inner: []*Component{
			{
				Name:     "child",
				Services: []*Service{{Name: "svc3"}},
			},
		},
	}

	names := make(map[string]bool)
	collectServiceNames(root, names)

	require.Len(t, names, 3)
	require.True(t, names["svc1"])
	require.True(t, names["svc2"])
	require.True(t, names["svc3"])
}

func TestContainsRemovedServiceRef(t *testing.T) {
	removedServices := map[string]bool{
		"removed-svc": true,
		"another-svc": true,
	}

	tests := []struct {
		name     string
		arg      string
		expected bool
	}{
		{"contains reference", `{{Service "removed-svc" "http" "http" ""}}`, true},
		{"no reference", `{{Service "other-svc" "http" "http" ""}}`, false},
		{"plain string", "just a regular argument", false},
		{"another removed service", `--url={{Service "another-svc" "http" "http" ""}}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsRemovedServiceRef(tt.arg, removedServices)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestApplyServiceOverrides(t *testing.T) {
	svc := &Service{
		Name:  "test-svc",
		Image: "old-image",
		Tag:   "old-tag",
		Args:  []string{"old-arg"},
	}

	config := &YAMLServiceConfig{
		Image: "new-image",
		Tag:   "new-tag",
		Args:  []string{"new-arg1", "new-arg2"},
		Env:   map[string]string{"KEY": "value"},
	}

	applyServiceOverrides(svc, config, nil)

	require.Equal(t, "new-image", svc.Image)
	require.Equal(t, "new-tag", svc.Tag)
	require.Len(t, svc.Args, 2)
	require.Equal(t, "value", svc.Env["KEY"])
}

func TestApplyServiceOverrides_PartialOverride(t *testing.T) {
	svc := &Service{
		Name:  "test-svc",
		Image: "original-image",
		Tag:   "original-tag",
		Args:  []string{"original-arg"},
	}

	config := &YAMLServiceConfig{Tag: "new-tag"}

	applyServiceOverrides(svc, config, nil)

	require.Equal(t, "original-image", svc.Image)
	require.Equal(t, "new-tag", svc.Tag)
	require.Equal(t, []string{"original-arg"}, svc.Args)
}

func TestApplyDependsOn(t *testing.T) {
	svc := &Service{Name: "test-svc"}

	dependsOn := []string{"db:healthy", "cache:running", "other"}

	applyDependsOn(svc, dependsOn, nil)

	require.Len(t, svc.DependsOn, 3)
	require.Equal(t, "db", svc.DependsOn[0].Name)
	require.Equal(t, DependsOnConditionHealthy, svc.DependsOn[0].Condition)
	require.Equal(t, "cache", svc.DependsOn[1].Name)
	require.Equal(t, DependsOnConditionRunning, svc.DependsOn[1].Condition)
	require.Equal(t, "other", svc.DependsOn[2].Name)
	require.Equal(t, DependsOnConditionHealthy, svc.DependsOn[2].Condition)
}

func TestApplyDependsOn_ComponentServiceFormat(t *testing.T) {
	svc := &Service{Name: "test-svc"}

	// Test component.service format - should extract just the service name
	dependsOn := []string{"reth.el:healthy", "merger.merger-builder:running"}

	applyDependsOn(svc, dependsOn, nil)

	require.Len(t, svc.DependsOn, 2)
	require.Equal(t, "el", svc.DependsOn[0].Name)
	require.Equal(t, DependsOnConditionHealthy, svc.DependsOn[0].Condition)
	require.Equal(t, "merger-builder", svc.DependsOn[1].Name)
	require.Equal(t, DependsOnConditionRunning, svc.DependsOn[1].Condition)
}

func TestCreateServiceFromConfig(t *testing.T) {
	config := &YAMLServiceConfig{
		Image:      "test-image",
		Tag:        "v1.0.0",
		Entrypoint: "/bin/sh",
		Args:       []string{"-c", "echo hello"},
		Env:        map[string]string{"DEBUG": "true"},
		Ports:      map[string]int{"http": 8080},
		DependsOn:  []string{"db:healthy"},
	}

	svc := createServiceFromConfig("my-service", config, nil)

	require.Equal(t, "my-service", svc.Name)
	require.Equal(t, "test-image", svc.Image)
	require.Equal(t, "v1.0.0", svc.Tag)
	require.Equal(t, "/bin/sh", svc.Entrypoint)
	require.Len(t, svc.Args, 2)
	require.Equal(t, "true", svc.Env["DEBUG"])
	require.Len(t, svc.Ports, 1)
	require.Len(t, svc.DependsOn, 1)
}

func TestYamlReleaseToRelease(t *testing.T) {
	cfg := &YAMLReleaseConfig{
		Name:    "myapp",
		Org:     "myorg",
		Repo:    "myrepo",
		Version: "v1.0.0",
		Format:  "tar.gz",
	}

	rel := yamlReleaseToRelease(cfg)

	require.Equal(t, "myapp", rel.Name)
	require.Equal(t, "myorg", rel.Org)
	require.Equal(t, "myrepo", rel.Repo)
	require.Equal(t, "v1.0.0", rel.Version)
	require.Equal(t, "x86_64-unknown-linux-gnu", rel.Arch("linux", "amd64"))
	require.Equal(t, "aarch64-apple-darwin", rel.Arch("darwin", "arm64"))
}

func TestYamlReleaseToRelease_BinaryFormat(t *testing.T) {
	cfg := &YAMLReleaseConfig{
		Name:    "myapp",
		Org:     "myorg",
		Version: "v1.0.0",
		Format:  "binary",
	}

	rel := yamlReleaseToRelease(cfg)

	require.Empty(t, rel.Arch("linux", "amd64"))
}

func TestIsYAMLRecipeFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	ymlFile := filepath.Join(tmpDir, "recipe.yml")
	otherFile := filepath.Join(tmpDir, "recipe.txt")

	for _, f := range []string{yamlFile, ymlFile, otherFile} {
		require.NoError(t, os.WriteFile(f, []byte("test"), 0o644))
	}

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"yaml file", yamlFile, true},
		{"yml file", ymlFile, true},
		{"other file", otherFile, false},
		{"nonexistent", filepath.Join(tmpDir, "nonexistent.yaml"), false},
		{"directory", tmpDir, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, IsYAMLRecipeFile(tt.path))
		})
	}
}

func TestCleanupDependsOn(t *testing.T) {
	root := &Component{
		Name: "root",
		Services: []*Service{
			{
				Name: "app",
				DependsOn: []*DependsOn{
					{Name: "db", Condition: DependsOnConditionHealthy},
					{Name: "removed-svc", Condition: DependsOnConditionHealthy},
					{Name: "cache", Condition: DependsOnConditionRunning},
				},
				Args: []string{
					"--db-url", "postgres://localhost",
					"--removed-url", `{{Service "removed-svc" "http" "http" ""}}`,
					"--cache-url", "redis://localhost",
				},
			},
		},
	}

	removedServices := map[string]bool{"removed-svc": true}

	cleanupDependsOn(root, removedServices)

	svc := root.Services[0]
	require.Len(t, svc.DependsOn, 2)
	for _, dep := range svc.DependsOn {
		require.NotEqual(t, "removed-svc", dep.Name)
	}
	for _, arg := range svc.Args {
		require.False(t, containsRemovedServiceRef(arg, removedServices))
	}
}

func TestApplyFilesToService(t *testing.T) {
	svc := &Service{Name: "test-svc"}

	files := map[string]string{
		"/app/config.json":  "artifact:config.json",
		"/app/genesis.json": "genesis.json",
	}

	applyFilesToService(svc, files)

	require.Len(t, svc.FilesMapped, 2)
	require.Equal(t, "config.json", svc.FilesMapped["/app/config.json"])
	require.Equal(t, "genesis.json", svc.FilesMapped["/app/genesis.json"])
}

func TestRemoveService_Nested(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{
				Name: "child",
				Services: []*Service{
					{Name: "nested-svc1"},
					{Name: "nested-svc2"},
				},
			},
		},
	}

	removeService(root, "nested-svc1")

	require.Len(t, root.Inner[0].Services, 1)
	require.Equal(t, "nested-svc2", root.Inner[0].Services[0].Name)
}

func TestApplyServiceOverrides_AllFields(t *testing.T) {
	svc := &Service{
		Name:  "test-svc",
		Image: "old-image",
	}

	config := &YAMLServiceConfig{
		Image:      "new-image",
		Tag:        "v2.0.0",
		Entrypoint: "/bin/bash",
		Ports:      map[string]int{"http": 8080, "https": 8443},
		Volumes:    map[string]string{"/data": "myvolume"},
		DependsOn:  []string{"db:healthy"},
		HostPath:   "/usr/local/bin/app",
		Release: &YAMLReleaseConfig{
			Name:    "myapp",
			Org:     "myorg",
			Version: "v1.0.0",
		},
	}

	applyServiceOverrides(svc, config, nil)

	require.Equal(t, "new-image", svc.Image)
	require.Equal(t, "v2.0.0", svc.Tag)
	require.Equal(t, "/bin/bash", svc.Entrypoint)
	require.Len(t, svc.Ports, 2)
	require.NotNil(t, svc.VolumesMapped)
	require.Len(t, svc.DependsOn, 1)
	require.Equal(t, "/usr/local/bin/app", svc.HostPath)
}

func TestYamlReleaseToRelease_DarwinAmd64(t *testing.T) {
	cfg := &YAMLReleaseConfig{
		Name:    "myapp",
		Org:     "myorg",
		Version: "v1.0.0",
		Format:  "tar.gz",
	}

	rel := yamlReleaseToRelease(cfg)

	require.Equal(t, "x86_64-apple-darwin", rel.Arch("darwin", "amd64"))
}

func TestYamlReleaseToRelease_UnknownOS(t *testing.T) {
	cfg := &YAMLReleaseConfig{
		Name:    "myapp",
		Org:     "myorg",
		Version: "v1.0.0",
		Format:  "tar.gz",
	}

	rel := yamlReleaseToRelease(cfg)

	require.Empty(t, rel.Arch("windows", "amd64"))
}

func TestCreateServiceFromConfig_WithHostPath(t *testing.T) {
	config := &YAMLServiceConfig{
		Image:    "test-image",
		HostPath: "/usr/local/bin/myapp",
	}

	svc := createServiceFromConfig("my-service", config, nil)

	require.Equal(t, "/usr/local/bin/myapp", svc.HostPath)
}

func TestCreateServiceFromConfig_WithRelease(t *testing.T) {
	config := &YAMLServiceConfig{
		Image: "test-image",
		Release: &YAMLReleaseConfig{
			Name:    "myapp",
			Org:     "myorg",
			Version: "v1.0.0",
		},
	}

	svc := createServiceFromConfig("my-service", config, nil)

	require.NotNil(t, svc.release)
	require.Equal(t, "myapp", svc.release.Name)
}

func TestCreateServiceFromConfig_WithVolumes(t *testing.T) {
	config := &YAMLServiceConfig{
		Image:   "test-image",
		Volumes: map[string]string{"/data": "myvolume"},
	}

	svc := createServiceFromConfig("my-service", config, nil)

	require.NotNil(t, svc.VolumesMapped)
	require.Equal(t, "myvolume", svc.VolumesMapped["/data"])
}

func TestParseYAMLRecipe(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe:
  test-component:
    services:
      test-svc:
        image: test-image
        tag: v1.0.0
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)

	require.NoError(t, err)
	require.NotNil(t, recipe)
	require.Contains(t, recipe.Name(), "yaml(")
	require.Contains(t, recipe.Description(), "l1")
}

func TestParseYAMLRecipe_MissingBase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `recipe:
  test-component:
    services:
      test-svc:
        image: test-image
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	_, err = ParseYAMLRecipe(yamlFile, baseRecipes)

	require.Error(t, err)
	require.Contains(t, err.Error(), "must specify a 'base' recipe")
}

func TestParseYAMLRecipe_UnknownBase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: unknown-recipe
recipe: {}
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	_, err = ParseYAMLRecipe(yamlFile, baseRecipes)

	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown base recipe")
}

func TestParseYAMLRecipe_FileNotFound(t *testing.T) {
	baseRecipes := []Recipe{&L1Recipe{}}

	_, err := ParseYAMLRecipe("/nonexistent/path/recipe.yaml", baseRecipes)

	require.Error(t, err)
}

func TestYAMLRecipe_Methods(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe: {}
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	require.NotNil(t, recipe.Flags())
	require.NotNil(t, recipe.Artifacts())
}

func TestYAMLRecipe_Apply(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe:
  reth:
    services:
      el:
        tag: v2.0.0
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	out, err := NewOutput(tmpDir)
	require.NoError(t, err)
	ctx := &ExContext{
		LogLevel:  LevelInfo,
		Contender: &ContenderContext{Enabled: false},
		Output:    out,
	}

	component := recipe.Apply(ctx)

	require.NotNil(t, component)
}

func TestYAMLRecipe_ApplyModifications_RemoveComponent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe:
  lighthouse-validator-node:
    remove: true
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	out, err := NewOutput(tmpDir)
	require.NoError(t, err)
	ctx := &ExContext{
		LogLevel:  LevelInfo,
		Contender: &ContenderContext{Enabled: false},
		Output:    out,
	}

	component := recipe.Apply(ctx)

	require.NotNil(t, component)
	require.Nil(t, findComponent(component, "lighthouse-validator-node"))
}

func TestYAMLRecipe_ApplyModifications_RemoveService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe:
  mev-boost-relay:
    services:
      mev-boost-relay:
        remove: true
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	out, err := NewOutput(tmpDir)
	require.NoError(t, err)
	ctx := &ExContext{
		LogLevel:  LevelInfo,
		Contender: &ContenderContext{Enabled: false},
		Output:    out,
	}

	component := recipe.Apply(ctx)

	require.NotNil(t, component)
	require.Nil(t, findService(component, "mev-boost-relay"))
}

func TestYAMLRecipe_ApplyModifications_AddNewService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe:
  new-component:
    services:
      new-svc:
        image: new-image
        tag: v1.0.0
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	out, err := NewOutput(tmpDir)
	require.NoError(t, err)
	ctx := &ExContext{
		LogLevel:  LevelInfo,
		Contender: &ContenderContext{Enabled: false},
		Output:    out,
	}

	component := recipe.Apply(ctx)

	require.NotNil(t, component)
	newSvc := findService(component, "new-svc")
	require.NotNil(t, newSvc)
	require.Equal(t, "new-image", newSvc.Image)
}

func TestCleanupDependsOn_WithNodeRefs(t *testing.T) {
	root := &Component{
		Name: "root",
		Services: []*Service{
			{
				Name: "app",
				NodeRefs: []*NodeRef{
					{Service: "db", PortLabel: "http"},
					{Service: "removed-svc", PortLabel: "http"},
				},
			},
		},
	}

	removedServices := map[string]bool{"removed-svc": true}

	cleanupDependsOn(root, removedServices)

	svc := root.Services[0]
	require.Len(t, svc.NodeRefs, 1)
	require.Equal(t, "db", svc.NodeRefs[0].Service)
}

func TestCleanupDependsOn_Nested(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{
				Name: "child",
				Services: []*Service{
					{
						Name: "nested-app",
						DependsOn: []*DependsOn{
							{Name: "removed-svc", Condition: DependsOnConditionHealthy},
						},
					},
				},
			},
		},
	}

	removedServices := map[string]bool{"removed-svc": true}

	cleanupDependsOn(root, removedServices)

	svc := root.Inner[0].Services[0]
	require.Empty(t, svc.DependsOn)
}

func TestYAMLRecipe_Output(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlContent := `base: l1
recipe: {}
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	result := recipe.Output(nil)
	require.NotNil(t, result)
}

func TestYAMLRecipe_Artifacts_WithExtraFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recipe-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a local file to reference
	localFile := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(localFile, []byte("test config"), 0o644))

	yamlContent := `base: l1
recipe:
  test-component:
    services:
      test-svc:
        image: test-image
        files:
          "/app/config.toml": "config.toml"
`
	yamlFile := filepath.Join(tmpDir, "recipe.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(yamlContent), 0o644))

	baseRecipes := []Recipe{&L1Recipe{}}

	recipe, err := ParseYAMLRecipe(yamlFile, baseRecipes)
	require.NoError(t, err)

	artifacts := recipe.Artifacts()
	require.NotNil(t, artifacts)
}

func TestApplyDependsOn_UnknownCondition(t *testing.T) {
	svc := &Service{Name: "test-svc"}

	dependsOn := []string{"db:unknown-condition"}

	applyDependsOn(svc, dependsOn, nil)

	require.Len(t, svc.DependsOn, 1)
	require.Equal(t, "db", svc.DependsOn[0].Name)
	require.Equal(t, DependsOnConditionHealthy, svc.DependsOn[0].Condition)
}
