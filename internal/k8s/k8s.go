package k8s

import (
	"fmt"

	"github.com/ferranbt/builder-playground/internal"
	"github.com/spf13/cobra"
)

var K8sCmd = &cobra.Command{
	Use:   "k8s",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("expected 1 argument, got %d", len(args))
		}

		outputPath := args[0]

		manifest, err := internal.ReadManifest(outputPath)
		if err != nil {
			return fmt.Errorf("failed to read manifest: %w", err)
		}
		fmt.Println(manifest)

		return nil
	},
}
