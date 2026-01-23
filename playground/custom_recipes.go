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
// Custom recipe names can be:
// - "name" for recipes directly under custom-recipes/ (e.g., custom-recipes/foo.yaml -> "foo")
// - "dir/name" for recipes in subdirectories (e.g., custom-recipes/rbuilder/bin.yaml -> "rbuilder/bin")
func GetEmbeddedCustomRecipes() ([]string, error) {
	var customRecipes []string
	entries, err := fs.ReadDir(CustomRecipesFS, "custom-recipes")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// Recipes in subdirectories: dir/name format
			dirName := entry.Name()
			subEntries, err := fs.ReadDir(CustomRecipesFS, filepath.Join("custom-recipes", dirName))
			if err != nil {
				continue
			}
			for _, subEntry := range subEntries {
				if !subEntry.IsDir() && (strings.HasSuffix(subEntry.Name(), ".yaml") || strings.HasSuffix(subEntry.Name(), ".yml")) {
					baseName := strings.TrimSuffix(subEntry.Name(), filepath.Ext(subEntry.Name()))
					customRecipes = append(customRecipes, dirName+"/"+baseName)
				}
			}
		} else if strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml") {
			// Recipes directly under custom-recipes/: name format
			baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			customRecipes = append(customRecipes, baseName)
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
func GetCustomRecipeInfo(customRecipeName string, baseRecipes []Recipe) (*CustomRecipeInfo, error) {
	parts := strings.SplitN(customRecipeName, "/", 2)

	var recipeDir, baseName string
	if len(parts) == 2 {
		recipeDir = parts[0]
		baseName = parts[1]
	} else {
		recipeDir = ""
		baseName = parts[0]
	}

	// Try to read the yaml file
	var content []byte
	var err error
	for _, ext := range []string{".yaml", ".yml"} {
		var yamlPath string
		if recipeDir != "" {
			yamlPath = filepath.Join("custom-recipes", recipeDir, baseName+ext)
		} else {
			yamlPath = filepath.Join("custom-recipes", baseName+ext)
		}
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
// customRecipeName can be:
// - "name" for recipes directly under custom-recipes/ (e.g., "foo" -> custom-recipes/foo.yaml)
// - "dir/name" for recipes in subdirectories (e.g., "rbuilder/bin" -> custom-recipes/rbuilder/bin.yaml)
func parseCustomRecipeName(customRecipeName string) (recipeDir, yamlFile, recipePath string, err error) {
	parts := strings.SplitN(customRecipeName, "/", 2)

	var baseName string
	if len(parts) == 2 {
		// Format: dir/name
		recipeDir = parts[0]
		baseName = parts[1]
		recipePath = filepath.Join("custom-recipes", recipeDir)
	} else {
		// Format: name (directly under custom-recipes/)
		recipeDir = ""
		baseName = parts[0]
		recipePath = "custom-recipes"
	}

	// Find the yaml file
	for _, ext := range []string{".yaml", ".yml"} {
		candidate := baseName + ext
		var yamlPath string
		if recipeDir != "" {
			yamlPath = filepath.Join("custom-recipes", recipeDir, candidate)
		} else {
			yamlPath = filepath.Join("custom-recipes", candidate)
		}
		if _, err := fs.ReadFile(CustomRecipesFS, yamlPath); err == nil {
			yamlFile = candidate
			break
		}
	}
	if yamlFile == "" {
		return "", "", "", fmt.Errorf("custom recipe '%s' not found. Run 'playground recipes' to see available options", customRecipeName)
	}

	return
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

		// Determine output filename
		outPath := fileName
		if fileName == yamlFile {
			outPath = "playground.yaml"
		}

		// Write the file to target directory
		fullPath := filepath.Join(targetDir, outPath)
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

		// Determine output filename
		outPath := fileName
		if fileName == yamlFile {
			outPath = "playground.yaml"
		}
		files = append(files, outPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan custom recipe: %w", err)
	}
	return files, nil
}
