package playground

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/goccy/go-yaml"
	flag "github.com/spf13/pflag"
)

var _ Recipe = &BuilderNetRecipe{}

// BuilderNetRecipe is a recipe that extends the L1 recipe to include builder-hub
type BuilderNetRecipe struct {
	// Embed the L1Recipe to reuse its functionality
	l1Recipe L1Recipe

	builderIP     string
	builderConfig string
}

func (b *BuilderNetRecipe) Name() string {
	return "buildernet"
}

func (b *BuilderNetRecipe) Description() string {
	return "Deploy a full L1 stack with mev-boost and builder-hub"
}

func (b *BuilderNetRecipe) Flags() *flag.FlagSet {
	// Reuse the L1Recipe flags
	flags := b.l1Recipe.Flags()
	flags.StringVar(&b.builderIP, "builder-ip", "127.0.0.1", "Ip of the external builder to register in BuilderHup")
	flags.StringVar(&b.builderConfig, "builder-config", "", "Config in YAML for the builder")
	return flags
}

func (b *BuilderNetRecipe) Artifacts() *ArtifactsBuilder {
	// Reuse the L1Recipe artifacts builder
	return b.l1Recipe.Artifacts()
}

func (b *BuilderNetRecipe) Apply(svcManager *Manifest) {
	// Start with the L1Recipe manifest
	b.l1Recipe.Apply(svcManager)

	svcManager.AddComponent(&BuilderHub{
		BuilderIP:     b.builderIP,
		BuilderConfig: b.builderConfig,
	})

	svcManager.RunContenderIfEnabled()
}

func (b *BuilderNetRecipe) Output(manifest *Manifest) map[string]interface{} {
	// Start with the L1Recipe output
	output := b.l1Recipe.Output(manifest)

	// Add builder-hub service info
	builderHubService := manifest.MustGetService("builder-hub-api")
	builderHubProxy := manifest.MustGetService("builder-hub-proxy")

	http := builderHubProxy.MustGetPort("http")
	admin := builderHubService.MustGetPort("admin")
	internal := builderHubService.MustGetPort("internal")

	output["builder-hub-proxy"] = fmt.Sprintf("http://localhost:%d", http.HostPort)
	output["builder-hub-admin"] = fmt.Sprintf("http://localhost:%d", admin.HostPort)
	output["builder-hub-internal"] = fmt.Sprintf("http://localhost:%d", internal.HostPort)

	return output
}

func postRequest(endpoint, path string, input interface{}) error {
	var data []byte
	if dataBytes, ok := input.([]byte); ok {
		data = dataBytes
	} else if dataMap, ok := input.(map[string]interface{}); ok {
		dataBytes, err := json.Marshal(dataMap)
		if err != nil {
			return err
		}
		data = dataBytes
	} else if dataStr, ok := input.(string); ok {
		data = []byte(dataStr)
	} else {
		return fmt.Errorf("input type not expected")
	}

	fullEndpoint := endpoint + path

	resp, err := http.Post(fullEndpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to request endpoint '%s': %v", fullEndpoint, err)
	}
	defer resp.Body.Close()

	dataResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("incorrect status code %s: %s", resp.Status, string(dataResp))
	}
	return nil
}

type builderHubRegisterBuilderInput struct {
	BuilderID     string
	BuilderIP     string
	MeasurementID string
	Network       string
	Config        string
}

func registerBuilder(httpEndpoint string, input *builderHubRegisterBuilderInput) error {
	httpEndpoint = httpEndpoint + "/api/admin/v1"

	// Validate input.Config, it must be a valid json file
	var configMap map[string]interface{}
	if err := json.Unmarshal([]byte(input.Config), &configMap); err != nil {
		return err
	}

	// Create Allow-All Measurements
	err := postRequest(httpEndpoint, "/measurements", map[string]interface{}{
		"measurement_id":   input.MeasurementID,
		"attestation_type": "test",
		"measurements":     map[string]interface{}{},
	})
	if err != nil {
		return err
	}

	// Enable Measurements
	err = postRequest(httpEndpoint, fmt.Sprintf("/measurements/activation/%s", input.MeasurementID), map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		return err
	}

	// create the builder
	err = postRequest(httpEndpoint, "/builders", map[string]interface{}{
		"name":       input.BuilderID,
		"ip_address": input.BuilderIP,
		"network":    input.Network,
	})
	if err != nil {
		return err
	}

	// Create Builder Configuration
	err = postRequest(httpEndpoint, fmt.Sprintf("/builders/configuration/%s", input.BuilderID), input.Config)
	if err != nil {
		return err
	}

	// Enable Builder
	err = postRequest(httpEndpoint, fmt.Sprintf("/builders/activation/%s", input.BuilderID), map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		return err
	}

	return nil
}

// YAMLToJSON converts a YAML string to a JSON string
func yamlToJson(yamlStr []byte) ([]byte, error) {
	// Unmarshal YAML into a map
	var data interface{}
	err := yaml.Unmarshal(yamlStr, &data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if data == nil {
		return []byte("{}"), nil
	}

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return jsonBytes, nil
}
