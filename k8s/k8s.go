package k8s

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/flashbots/builder-playground/playground"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/cobra"
)

//go:embed templates/*
var templates embed.FS

var outputFlag string

func init() {
	K8sCommand.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")
}

var K8sCommand = &cobra.Command{
	Use:   "k8s",
	Short: "Kubernetes commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		var err error
		if outputFlag == "" {
			if outputFlag, err = playground.GetHomeDir(); err != nil {
				return err
			}
		}

		manifest, err := playground.ReadManifest(outputFlag)
		if err != nil {
			return err
		}

		out, err := applyTemplate("namespace", manifest)
		if err != nil {
			return err
		}
		fmt.Println(out)

		for _, svc := range manifest.Services {
			if err := createService(manifest, svc); err != nil {
				return err
			}
		}

		return nil
	},
}

func createService(manifest *playground.Manifest, svc *playground.Service) error {
	funcs := template.FuncMap{
		"Service": func(name string, portLabel, protocol, user string) string {
			target := manifest.MustGetService(name)
			return playground.PrintAddr(protocol, name, target.MustGetPort(portLabel).Port, user)
		},
		"Port": func(name string, defaultPort int) int {
			return defaultPort
		},
		"PortUDP": func(name string, defaultPort int) int {
			return defaultPort
		},
	}

	newArgs, err := svc.ReplaceArgs(funcs)
	if err != nil {
		return fmt.Errorf("failed to replace args: %w", err)
	}
	svc.Args = newArgs

	var input map[string]interface{}
	if err := mapstructure.Decode(svc, &input); err != nil {
		return fmt.Errorf("failed to decode service: %w", err)
	}

	// add more context data
	input["Namespace"] = manifest.Name

	res, err := applyTemplate("deployment", input)
	if err != nil {
		return fmt.Errorf("failed to apply service template: %w", err)
	}
	fmt.Println(res)

	return nil
}

func applyTemplate(templateName string, input interface{}) (string, error) {
	content, err := templates.ReadFile(fmt.Sprintf("templates/%s.yaml.tmpl", templateName))
	if err != nil {
		return "", fmt.Errorf("failed to open template: %w", err)
	}

	tmpl, err := template.New(templateName).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}
	return buf.String(), nil
}
