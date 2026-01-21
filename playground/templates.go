package playground

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TemplatesFS holds the embedded templates filesystem, set by main package
var TemplatesFS embed.FS

// GetEmbeddedTemplates returns a list of template names from the embedded templates
// Template names are in the format "dir/filename" (without extension)
func GetEmbeddedTemplates() ([]string, error) {
	var templates []string
	entries, err := TemplatesFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			dirName := entry.Name()
			subEntries, err := TemplatesFS.ReadDir(filepath.Join("templates", dirName))
			if err != nil {
				continue
			}
			for _, subEntry := range subEntries {
				if !subEntry.IsDir() && (strings.HasSuffix(subEntry.Name(), ".yaml") || strings.HasSuffix(subEntry.Name(), ".yml")) {
					// Template name is dir/filename without extension
					baseName := strings.TrimSuffix(subEntry.Name(), filepath.Ext(subEntry.Name()))
					templates = append(templates, dirName+"/"+baseName)
				}
			}
		}
	}
	return templates, nil
}

// GenerateFromTemplate extracts a template and its dependencies to current directory
// templateName should be in the format "dir/filename" (e.g., "rbuilder/custom")
// If force is false, it will error if any files already exist
func GenerateFromTemplate(templateName string, force bool) error {
	// Parse the template name (format: dir/filename)
	parts := strings.SplitN(templateName, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid template name '%s', expected format 'dir/name'. Run 'playground recipes' to see available options", templateName)
	}
	templateDir := parts[0]
	baseName := parts[1]

	// Check if the yaml file exists
	yamlFile := baseName + ".yaml"
	yamlPath := filepath.Join("templates", templateDir, yamlFile)
	if _, err := TemplatesFS.ReadFile(yamlPath); err != nil {
		// Try .yml extension
		yamlFile = baseName + ".yml"
		yamlPath = filepath.Join("templates", templateDir, yamlFile)
		if _, err := TemplatesFS.ReadFile(yamlPath); err != nil {
			return fmt.Errorf("template '%s' not found. Run 'playground recipes' to see available options", templateName)
		}
	}

	// First pass: collect files to write and check for existing files
	templatePath := filepath.Join("templates", templateDir)
	var filesToWrite []string
	err := fs.WalkDir(TemplatesFS, templatePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		fileName := filepath.Base(path)

		// Skip other yaml files that aren't the selected template
		if (strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yml")) && fileName != yamlFile {
			return nil
		}

		// Determine output filename
		outPath := fileName
		if fileName == yamlFile {
			outPath = "playground.yaml"
		}
		filesToWrite = append(filesToWrite, outPath)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to scan template: %w", err)
	}

	// Check for existing files if not forcing
	if !force {
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

	// Second pass: write files
	err = fs.WalkDir(TemplatesFS, templatePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		fileName := filepath.Base(path)

		// Skip other yaml files that aren't the selected template
		if (strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yml")) && fileName != yamlFile {
			return nil
		}

		// Read the file content
		content, err := TemplatesFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Determine output filename
		outPath := fileName

		// Rename the yaml file to playground.yaml
		if fileName == yamlFile {
			outPath = "playground.yaml"
		}

		// Write the file
		if err := os.WriteFile(outPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", outPath, err)
		}
		fmt.Printf("Created %s\n", outPath)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to extract template: %w", err)
	}

	return nil
}
