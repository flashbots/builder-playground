package playground

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	flag "github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func newTestCustomRecipesFS() fstest.MapFS {
	return fstest.MapFS{
		// Recipes in subdirectory (dir/name format)
		"custom-recipes/testdir/sample.yaml": &fstest.MapFile{
			Data: []byte(`base: l1
description: A sample test recipe

recipe:
  reth:
    services:
      el:
        image: "test-image:latest"
`),
		},
		"custom-recipes/testdir/another.yml": &fstest.MapFile{
			Data: []byte(`base: l1
description: Another test recipe
`),
		},
		// Recipe directly under custom-recipes/ (name format)
		"custom-recipes/rootrecipe.yaml": &fstest.MapFile{
			Data: []byte(`base: l1
description: A root level recipe
`),
		},
	}
}

func setupTestCustomRecipesFS(t *testing.T) func() {
	t.Helper()
	original := CustomRecipesFS
	CustomRecipesFS = newTestCustomRecipesFS()
	return func() {
		CustomRecipesFS = original
	}
}

func TestGetEmbeddedCustomRecipes(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	recipes, err := GetEmbeddedCustomRecipes()
	require.NoError(t, err)
	require.NotEmpty(t, recipes)

	// Should find recipes in subdirectory (dir/name format)
	require.Contains(t, recipes, "testdir/sample")
	require.Contains(t, recipes, "testdir/another")

	// Should find recipes at root level (name format)
	require.Contains(t, recipes, "rootrecipe")
}

func TestGetCustomRecipeInfo(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Create a mock base recipe for testing
	mockRecipes := []Recipe{
		&mockRecipe{name: "l1", components: []string{"reth", "lighthouse"}},
	}

	info, err := GetCustomRecipeInfo("testdir/sample", mockRecipes)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "testdir/sample", info.Name)
	require.Equal(t, "l1", info.Base)
	require.Equal(t, "A sample test recipe", info.Description)
}

func TestGetCustomRecipeInfo_RootLevel(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Test root level recipe (name format without dir/)
	info, err := GetCustomRecipeInfo("rootrecipe", nil)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "rootrecipe", info.Name)
	require.Equal(t, "l1", info.Base)
	require.Equal(t, "A root level recipe", info.Description)
}

func TestGetCustomRecipeInfo_NotFound(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	_, err := GetCustomRecipeInfo("testdir/nonexistent", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom recipe not found")
}

func TestParseCustomRecipeName(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	tests := []struct {
		name        string
		input       string
		wantDir     string
		wantYAML    string
		wantPath    string
		expectError bool
	}{
		{
			name:     "dir/name format",
			input:    "testdir/sample",
			wantDir:  "testdir",
			wantYAML: "sample.yaml",
			wantPath: "custom-recipes/testdir",
		},
		{
			name:     "root level recipe (name format)",
			input:    "rootrecipe",
			wantDir:  "",
			wantYAML: "rootrecipe.yaml",
			wantPath: "custom-recipes",
		},
		{
			name:        "not found - root level",
			input:       "nonexistent",
			expectError: true,
		},
		{
			name:        "not found - subdirectory",
			input:       "testdir/nonexistent",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, yamlFile, path, err := parseCustomRecipeName(tt.input)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantDir, dir)
				require.Equal(t, tt.wantYAML, yamlFile)
				require.Equal(t, tt.wantPath, path)
			}
		})
	}
}

func TestListCustomRecipeFiles(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	files, err := listCustomRecipeFiles("testdir/sample")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// Should include playground.yaml (renamed from sample.yaml)
	hasPlaygroundYAML := false
	for _, f := range files {
		if f == "playground.yaml" {
			hasPlaygroundYAML = true
			break
		}
	}
	require.True(t, hasPlaygroundYAML, "expected playground.yaml in files list")
}

func TestListCustomRecipeFiles_InvalidName(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	_, err := listCustomRecipeFiles("invalid")
	require.Error(t, err)
}

func TestGenerateCustomRecipeToDir(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Create a temp directory
	tmpDir, err := os.MkdirTemp("", "custom-recipe-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	yamlPath, err := GenerateCustomRecipeToDir("testdir/sample", tmpDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tmpDir, "playground.yaml"), yamlPath)

	// Verify the file was created
	_, err = os.Stat(yamlPath)
	require.NoError(t, err)

	// Read and verify content
	content, err := os.ReadFile(yamlPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "base: l1")
}

func TestGenerateCustomRecipeToDir_InvalidName(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	tmpDir, err := os.MkdirTemp("", "custom-recipe-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	_, err = GenerateCustomRecipeToDir("invalid", tmpDir)
	require.Error(t, err)
}

func TestGenerateFromCustomRecipe(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Create a temp directory and change to it
	tmpDir, err := os.MkdirTemp("", "custom-recipe-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save current dir and change to temp
	origDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(origDir)

	err = GenerateFromCustomRecipe("testdir/sample", false)
	require.NoError(t, err)

	// Verify playground.yaml was created
	_, err = os.Stat("playground.yaml")
	require.NoError(t, err)
}

func TestGenerateFromCustomRecipe_ExistingFile(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Create a temp directory and change to it
	tmpDir, err := os.MkdirTemp("", "custom-recipe-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save current dir and change to temp
	origDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(origDir)

	// Create an existing file
	err = os.WriteFile("playground.yaml", []byte("existing"), 0o644)
	require.NoError(t, err)

	// Should fail without force
	err = GenerateFromCustomRecipe("testdir/sample", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "files already exist")

	// Should succeed with force
	err = GenerateFromCustomRecipe("testdir/sample", true)
	require.NoError(t, err)
}

func TestGetRecipeComponents(t *testing.T) {
	recipe := &mockRecipe{
		name:       "test",
		components: []string{"comp1", "comp2", "comp3"},
	}

	components := GetRecipeComponents(recipe)
	require.Equal(t, []string{"comp1", "comp2", "comp3"}, components)
}

func TestGetRecipeComponentsFormatted(t *testing.T) {
	tests := []struct {
		name       string
		components []string
		expected   string
	}{
		{
			name:       "simple components",
			components: []string{"comp1", "comp2"},
			expected:   "comp1, comp2",
		},
		{
			name:       "with nested recipe",
			components: []string{"l1-recipe", "builder-hub"},
			expected:   "l1 + builder-hub",
		},
		{
			name:       "nested recipe with multiple extras",
			components: []string{"l1-recipe", "builder-hub", "relay"},
			expected:   "l1 + builder-hub, relay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recipe := &mockRecipe{
				name:       "test",
				components: tt.components,
			}
			result := GetRecipeComponentsFormatted(recipe)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCollectComponentNames(t *testing.T) {
	tests := []struct {
		name     string
		input    *Component
		expected []string
	}{
		{
			name:     "nil component",
			input:    nil,
			expected: nil,
		},
		{
			name: "component with inner",
			input: &Component{
				Name: "root",
				Inner: []*Component{
					{Name: "child1"},
					{Name: "child2"},
				},
			},
			expected: []string{"child1", "child2"},
		},
		{
			name: "component without inner",
			input: &Component{
				Name:  "root",
				Inner: nil,
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectComponentNames(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// mockRecipe implements Recipe interface for testing
type mockRecipe struct {
	name        string
	description string
	components  []string
}

func (m *mockRecipe) Name() string {
	return m.name
}

func (m *mockRecipe) Description() string {
	return m.description
}

func (m *mockRecipe) Flags() *flag.FlagSet {
	return nil
}

func (m *mockRecipe) Artifacts() *ArtifactsBuilder {
	return nil
}

func (m *mockRecipe) Apply(ctx *ExContext) *Component {
	root := &Component{Name: m.name + "-recipe"}
	for _, c := range m.components {
		root.Inner = append(root.Inner, &Component{Name: c})
	}
	return root
}

func (m *mockRecipe) Output(manifest *Manifest) map[string]interface{} {
	return nil
}
