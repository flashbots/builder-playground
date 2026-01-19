package playground

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	flag "github.com/spf13/pflag"
)

func GenerateDocs(recipes []Recipe) error {
	// Create docs/recipes directory
	recipesDir := filepath.Join("docs", "recipes")
	if err := os.MkdirAll(recipesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create recipes directory: %w", err)
	}

	for _, recipe := range recipes {
		out, err := NewOutput("/tmp/docs-output")
		if err != nil {
			return err
		}

		components := recipe.Apply(&ExContext{Contender: &ContenderContext{}, Output: out})
		manifest := NewManifest("", components)

		// Generate markdown content
		var md strings.Builder

		// Title and description
		md.WriteString(fmt.Sprintf("# %s Recipe\n\n", recipe.Name()))
		md.WriteString(fmt.Sprintf("%s.\n\n", recipe.Description()))

		// Flags section
		md.WriteString("## Flags\n\n")
		flags := recipe.Flags()
		if flags != nil && flags.HasFlags() {
			flags.VisitAll(func(f *flag.Flag) {
				flagType := f.Value.Type()
				defaultVal := f.DefValue
				usage := f.Usage

				md.WriteString(fmt.Sprintf("- `%s` (%s): %s. Default to '%s'.\n", f.Name, flagType, usage, defaultVal))
			})
			md.WriteString("\n")
		} else {
			md.WriteString("No flags available.\n\n")
		}

		// Dot graph section
		md.WriteString("## Architecture Diagram\n\n")
		md.WriteString("```mermaid\n")
		md.WriteString(manifest.GenerateMermaidGraph())
		md.WriteString("```\n\n")

		// Write to file
		filename := filepath.Join(recipesDir, fmt.Sprintf("%s.md", recipe.Name()))
		if err := os.WriteFile(filename, []byte(md.String()), 0o644); err != nil {
			return fmt.Errorf("failed to write docs for recipe %s: %w", recipe.Name(), err)
		}

		fmt.Printf("Generated documentation: %s\n", filename)
	}

	return nil
}
