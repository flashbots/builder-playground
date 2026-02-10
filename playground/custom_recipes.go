package playground

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// CustomRecipesFS holds the embedded custom recipes filesystem, set by main package
var CustomRecipesFS fs.FS

// GetEmbeddedCustomRecipes returns a list of custom recipe names from the embedded custom recipes
// Custom recipe names are in "group/variant" format where the recipe is at:
// custom-recipes/group/variant/playground.yaml (e.g., custom-recipes/rbuilder/bin/playground.yaml -> "rbuilder/bin")
func GetEmbeddedCustomRecipes() ([]string, error) {
	var customRecipes []string
	entries, err := fs.ReadDir(CustomRecipesFS, "custom-recipes")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// First level: group directory (e.g., "rbuilder")
		groupName := entry.Name()
		subEntries, err := fs.ReadDir(CustomRecipesFS, filepath.Join("custom-recipes", groupName))
		if err != nil {
			continue
		}
		for _, subEntry := range subEntries {
			if !subEntry.IsDir() {
				continue
			}
			// Second level: variant directory (e.g., "bin" or "custom")
			variantName := subEntry.Name()
			// Check if playground.yaml exists in this variant directory
			for _, ext := range []string{".yaml", ".yml"} {
				yamlPath := filepath.Join("custom-recipes", groupName, variantName, "playground"+ext)
				if _, err := fs.Stat(CustomRecipesFS, yamlPath); err == nil {
					customRecipes = append(customRecipes, groupName+"/"+variantName)
					break
				}
			}
		}
	}
	return customRecipes, nil
}

// CustomRecipeInfo contains metadata about a custom recipe
type CustomRecipeInfo struct {
	Name               string
	Description        string
	Base               string
	ModifiedComponents []string
	NewComponents      []string
}

// GetCustomRecipeInfo returns metadata about a specific custom recipe
// customRecipeName should be in "group/variant" format (e.g., "rbuilder/bin")
func GetCustomRecipeInfo(customRecipeName string, baseRecipes []Recipe) (*CustomRecipeInfo, error) {
	parts := strings.SplitN(customRecipeName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid custom recipe name: %s (expected group/variant format)", customRecipeName)
	}
	groupName := parts[0]
	variantName := parts[1]

	// Try to read the playground.yaml file
	var content []byte
	var err error
	for _, ext := range []string{".yaml", ".yml"} {
		yamlPath := filepath.Join("custom-recipes", groupName, variantName, "playground"+ext)
		content, err = fs.ReadFile(CustomRecipesFS, yamlPath)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("custom recipe not found: %s", customRecipeName)
	}

	// Parse the YAML to extract metadata
	var config YAMLRecipeConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("failed to parse custom recipe: %w", err)
	}

	info := &CustomRecipeInfo{
		Name:        customRecipeName,
		Base:        config.Base,
		Description: config.Description,
	}

	// Find the base recipe to get its components
	var baseRecipe Recipe
	for _, r := range baseRecipes {
		if r.Name() == config.Base {
			baseRecipe = r
			break
		}
	}

	// Get base recipe component names
	baseComponents := make(map[string]bool)
	if baseRecipe != nil {
		for _, name := range GetRecipeComponents(baseRecipe) {
			baseComponents[name] = true
		}
	}

	// Categorize custom recipe components as modified or new
	for componentName := range config.Recipe {
		if baseComponents[componentName] {
			info.ModifiedComponents = append(info.ModifiedComponents, componentName)
		} else {
			info.NewComponents = append(info.NewComponents, componentName)
		}
	}

	return info, nil
}

// GetRecipeComponents returns the component names for a recipe
func GetRecipeComponents(recipe Recipe) []string {
	ctx := &ExContext{
		LogLevel:  LevelInfo,
		Contender: &ContenderContext{Enabled: false},
		Output: &output{
			dst: "/tmp/playground-list",
		},
	}
	component := recipe.Apply(ctx)
	return collectComponentNames(component)
}

// GetRecipeComponentsFormatted returns a formatted string of component names for a recipe
// If a component is itself a recipe (ends with "-recipe"), it formats as "base + extra1, extra2"
func GetRecipeComponentsFormatted(recipe Recipe) string {
	names := GetRecipeComponents(recipe)

	// Check if any component is a nested recipe (ends with "-recipe")
	var baseRecipe string
	var extras []string
	for _, name := range names {
		if strings.HasSuffix(name, "-recipe") {
			// Extract base name (e.g., "l1-recipe" -> "l1")
			baseRecipe = strings.TrimSuffix(name, "-recipe")
		} else {
			extras = append(extras, name)
		}
	}

	if baseRecipe != "" && len(extras) > 0 {
		return baseRecipe + " + " + strings.Join(extras, ", ")
	}
	return strings.Join(names, ", ")
}

func collectComponentNames(c *Component) []string {
	if c == nil {
		return nil
	}
	var names []string
	for _, inner := range c.Inner {
		names = append(names, inner.Name)
	}
	return names
}

// parseCustomRecipeName parses a custom recipe name and returns the recipe directory, yaml filename, and recipe path
// customRecipeName should be in "group/variant" format (e.g., "rbuilder/bin" -> custom-recipes/rbuilder/bin/playground.yaml)
func parseCustomRecipeName(customRecipeName string) (recipeDir, yamlFile, recipePath string, err error) {
	parts := strings.SplitN(customRecipeName, "/", 2)
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("custom recipe '%s' not found. Run 'playground recipes' to see available options", customRecipeName)
	}

	groupName := parts[0]
	variantName := parts[1]
	recipePath = filepath.Join("custom-recipes", groupName, variantName)

	// Find the playground.yaml file
	for _, ext := range []string{".yaml", ".yml"} {
		candidate := "playground" + ext
		yamlPath := filepath.Join(recipePath, candidate)
		if _, err := fs.ReadFile(CustomRecipesFS, yamlPath); err == nil {
			yamlFile = candidate
			break
		}
	}
	if yamlFile == "" {
		return "", "", "", fmt.Errorf("custom recipe '%s' not found. Run 'playground recipes' to see available options", customRecipeName)
	}

	return groupName + "/" + variantName, yamlFile, recipePath, nil
}

// GenerateCustomRecipeToDir extracts a custom recipe and its dependencies to the specified directory
// Returns the path to the generated playground.yaml file
func GenerateCustomRecipeToDir(customRecipeName, targetDir string) (string, error) {
	_, yamlFile, recipePath, err := parseCustomRecipeName(customRecipeName)
	if err != nil {
		return "", err
	}

	// Extract files to target directory
	err = fs.WalkDir(CustomRecipesFS, recipePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		fileName := filepath.Base(path)

		// Skip other yaml files that aren't the selected custom recipe
		if (strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yml")) && fileName != yamlFile {
			return nil
		}

		// Read the file content
		content, err := fs.ReadFile(CustomRecipesFS, path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Write the file to target directory (playground.yaml is already correctly named)
		fullPath := filepath.Join(targetDir, fileName)
		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", fullPath, err)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to extract custom recipe: %w", err)
	}

	return filepath.Join(targetDir, "playground.yaml"), nil
}

// GenerateFromCustomRecipe extracts a custom recipe and its dependencies to current directory
// customRecipeName should be in the format "dir/filename" (e.g., "rbuilder/custom")
// If force is false, it will error if any files already exist
func GenerateFromCustomRecipe(customRecipeName string, force bool) error {
	// Check for existing files if not forcing
	if !force {
		filesToWrite, err := listCustomRecipeFiles(customRecipeName)
		if err != nil {
			return err
		}
		var existingFiles []string
		for _, f := range filesToWrite {
			if _, err := os.Stat(f); err == nil {
				existingFiles = append(existingFiles, f)
			}
		}
		if len(existingFiles) > 0 {
			return fmt.Errorf("files already exist: %s. Use --force to overwrite", strings.Join(existingFiles, ", "))
		}
	}

	// Generate to current directory
	_, err := GenerateCustomRecipeToDir(customRecipeName, ".")
	if err != nil {
		return err
	}

	// Print created files
	filesToWrite, _ := listCustomRecipeFiles(customRecipeName)
	for _, f := range filesToWrite {
		fmt.Printf("Created %s\n", f)
	}
	return nil
}

// LoadCustomRecipe generates a custom recipe to a temp directory and parses it.
// Returns the parsed recipe and a cleanup function to remove the temp directory.
func LoadCustomRecipe(customRecipeName string, baseRecipes []Recipe) (Recipe, func(), error) {
	tmpDir, err := os.MkdirTemp("", "playground-custom-recipe-")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	yamlPath, err := GenerateCustomRecipeToDir(customRecipeName, tmpDir)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to generate custom recipe: %w", err)
	}

	recipe, err := ParseYAMLRecipe(yamlPath, baseRecipes)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to parse custom recipe: %w", err)
	}

	return recipe, cleanup, nil
}

// listCustomRecipeFiles returns the list of output files that would be created for a custom recipe
func listCustomRecipeFiles(customRecipeName string) ([]string, error) {
	_, yamlFile, recipePath, err := parseCustomRecipeName(customRecipeName)
	if err != nil {
		return nil, err
	}

	// Collect output files
	var files []string
	err = fs.WalkDir(CustomRecipesFS, recipePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		fileName := filepath.Base(path)

		// Skip other yaml files that aren't the selected custom recipe
		if (strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yml")) && fileName != yamlFile {
			return nil
		}

		// playground.yaml is already correctly named
		files = append(files, fileName)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan custom recipe: %w", err)
	}
	return files, nil
}
