package playground

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	flag "github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// getRepoRootFS returns an fs.FS rooted at the repository root for testing real custom-recipes
func getRepoRootFS(t *testing.T) fs.FS {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	// Go up from playground/ to repo root
	repoRoot := filepath.Dir(filepath.Dir(filename))
	return os.DirFS(repoRoot)
}

func newTestCustomRecipesFS() fstest.MapFS {
	return fstest.MapFS{
		// Recipes in group/variant/playground.yaml format
		"custom-recipes/testdir/sample/playground.yaml": &fstest.MapFile{
			Data: []byte(`base: l1
description: A sample test recipe

recipe:
  reth:
    services:
      el:
        image: "test-image:latest"
`),
		},
		"custom-recipes/testdir/sample/extra.toml": &fstest.MapFile{
			Data: []byte("[section]\nkey = \"value\"\n"),
		},
		"custom-recipes/testdir/another/playground.yaml": &fstest.MapFile{
			Data: []byte(`base: l1
description: Another test recipe
`),
		},
		// Another group with a variant
		"custom-recipes/othergroup/variant1/playground.yaml": &fstest.MapFile{
			Data: []byte(`base: l1
description: Other group recipe
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

	// Should find recipes in group/variant format
	require.Contains(t, recipes, "testdir/sample")
	require.Contains(t, recipes, "testdir/another")
	require.Contains(t, recipes, "othergroup/variant1")
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

func TestGetCustomRecipeInfo_OtherGroup(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Test another group/variant
	info, err := GetCustomRecipeInfo("othergroup/variant1", nil)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "othergroup/variant1", info.Name)
	require.Equal(t, "l1", info.Base)
	require.Equal(t, "Other group recipe", info.Description)
}

func TestGetCustomRecipeInfo_NotFound(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	_, err := GetCustomRecipeInfo("testdir/nonexistent", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom recipe not found")

	// Also test invalid format (no slash)
	_, err = GetCustomRecipeInfo("invalidformat", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid custom recipe name")
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
			name:     "group/variant format",
			input:    "testdir/sample",
			wantDir:  "testdir/sample",
			wantYAML: "playground.yaml",
			wantPath: "custom-recipes/testdir/sample",
		},
		{
			name:     "another group/variant",
			input:    "othergroup/variant1",
			wantDir:  "othergroup/variant1",
			wantYAML: "playground.yaml",
			wantPath: "custom-recipes/othergroup/variant1",
		},
		{
			name:        "invalid format - no slash",
			input:       "nonexistent",
			expectError: true,
		},
		{
			name:        "not found - variant doesn't exist",
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

	// Should include playground.yaml and non-YAML sibling files
	require.Contains(t, files, "playground.yaml")
	require.Contains(t, files, "extra.toml")
}

func TestListCustomRecipeFiles_InvalidName(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Invalid format (no slash)
	_, err := listCustomRecipeFiles("invalid")
	require.Error(t, err)

	// Valid format but doesn't exist
	_, err = listCustomRecipeFiles("testdir/nonexistent")
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

	// Verify non-YAML sibling file was also extracted
	extraPath := filepath.Join(tmpDir, "extra.toml")
	extraContent, err := os.ReadFile(extraPath)
	require.NoError(t, err)
	require.Contains(t, string(extraContent), "key = \"value\"")
}

func TestGenerateCustomRecipeToDir_InvalidName(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	tmpDir, err := os.MkdirTemp("", "custom-recipe-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Invalid format (no slash)
	_, err = GenerateCustomRecipeToDir("invalid", tmpDir)
	require.Error(t, err)

	// Valid format but doesn't exist
	_, err = GenerateCustomRecipeToDir("testdir/nonexistent", tmpDir)
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

func TestLoadCustomRecipe(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	mockRecipes := []Recipe{
		&mockRecipe{name: "l1", components: []string{"reth", "lighthouse"}},
	}

	recipe, cleanupTmp, err := LoadCustomRecipe("testdir/sample", mockRecipes)
	require.NoError(t, err)
	require.NotNil(t, recipe)
	require.NotNil(t, cleanupTmp)
	defer cleanupTmp()

	// Verify the recipe was parsed correctly
	require.Contains(t, recipe.Name(), "playground.yaml")
}

func TestLoadCustomRecipe_NotFound(t *testing.T) {
	cleanup := setupTestCustomRecipesFS(t)
	defer cleanup()

	// Invalid format (no slash)
	_, cleanupTmp, err := LoadCustomRecipe("nonexistent", nil)
	require.Error(t, err)
	require.Nil(t, cleanupTmp)

	// Valid format but doesn't exist
	_, cleanupTmp, err = LoadCustomRecipe("testdir/nonexistent", nil)
	require.Error(t, err)
	require.Nil(t, cleanupTmp)
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
	return flag.NewFlagSet("", 0)
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

func TestValidateBaseRecipes(t *testing.T) {
	baseRecipes := GetBaseRecipes()
	require.NotEmpty(t, baseRecipes)

	for _, recipe := range baseRecipes {
		t.Run(recipe.Name(), func(t *testing.T) {
			result := ValidateRecipe(recipe, baseRecipes)
			require.Empty(t, result.Errors, "base recipe %s has validation errors: %v", recipe.Name(), result.Errors)
		})
	}
}

func TestValidateShippedCustomRecipes(t *testing.T) {
	// Validate real custom-recipes that ship with the binary
	original := CustomRecipesFS
	CustomRecipesFS = getRepoRootFS(t)
	defer func() { CustomRecipesFS = original }()

	baseRecipes := GetBaseRecipes()

	customRecipes, err := GetEmbeddedCustomRecipes()
	require.NoError(t, err)
	require.NotEmpty(t, customRecipes)

	for _, name := range customRecipes {
		t.Run(name, func(t *testing.T) {
			recipe, cleanup, err := LoadCustomRecipe(name, baseRecipes)
			require.NoError(t, err)
			defer cleanup()

			result := ValidateRecipe(recipe, baseRecipes)

			// Filter host_path errors (environment-specific)
			var errs []string
			for _, e := range result.Errors {
				if !strings.Contains(e, "host_path does not exist") {
					errs = append(errs, e)
				}
			}
			require.Empty(t, errs, "validation errors: %v", errs)
		})
	}
}
