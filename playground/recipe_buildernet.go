package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	flag "github.com/spf13/pflag"
)

const BuilderHostIPAddress = "10.0.2.2"

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
	flags.StringVar(&b.builderIP, "builder-ip", "127.0.0.1", "IP address of the external builder to register in BuilderHub")
	flags.StringVar(&b.builderConfig, "builder-config", "", "Builder config in YAML format")
	return flags
}

func (b *BuilderNetRecipe) Artifacts() *ArtifactsBuilder {
	// Reuse the L1Recipe artifacts builder
	return b.l1Recipe.Artifacts()
}

func (b *BuilderNetRecipe) Apply(ctx *ExContext) *Component {
	component := NewComponent("buildernet-recipe")

	// Start with the L1Recipe manifest
	component.AddComponent(ctx, &b.l1Recipe)
	component.AddComponent(ctx, &BuilderHub{
		BuilderIP:     b.builderIP,
		BuilderConfig: b.builderConfig,
	})

	component.AddComponent(ctx, &Fileserver{})

	// Apply beacon service overrides for buildernet.
	// We need these for letting the builder connect to the beacon node.
	// Basically, the beacon node can never be healthy until the builder
	// connects.
	if beacon := component.FindService("beacon"); beacon != nil {
		beacon.ReplaceArgs(map[string]string{
			"--target-peers": "1",
		})
		beacon.WithArgs("--subscribe-all-subnets")
	}
	if mevBoostRelay := component.FindService("mev-boost-relay"); mevBoostRelay != nil {
		mevBoostRelay.DependsOnNone()
	}
	// Remove beacon healthmon - doesn't work with --target-peers=1 which is required for builder VM
	component.RemoveService("beacon_healthmon")

	component.RunContenderIfEnabled(ctx)

	return component
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

func postRequest(endpoint, path string, input interface{}) ([]byte, error) {
	var data []byte
	if dataBytes, ok := input.([]byte); ok {
		data = dataBytes
	} else if dataMap, ok := input.(map[string]interface{}); ok {
		dataBytes, err := json.Marshal(dataMap)
		if err != nil {
			return nil, err
		}
		data = dataBytes
	} else if dataStr, ok := input.(string); ok {
		data = []byte(dataStr)
	} else {
		return nil, fmt.Errorf("input type not expected")
	}

	fullEndpoint := endpoint + path

	resp, err := http.Post(fullEndpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to request endpoint '%s': %v", fullEndpoint, err)
	}
	defer resp.Body.Close()

	dataResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return dataResp, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("incorrect status code %s: %s", resp.Status, string(dataResp))
	}
	return dataResp, nil
}

type builderHubRegisterBuilderInput struct {
	BuilderID     string
	BuilderIP     string
	MeasurementID string
	Network       string
	Config        string
}

type identityResponse struct {
	Data struct {
		PeerID string `json:"peer_id"`
	} `json:"data"`
}

type enodeResponse struct {
	Result struct {
		Enode string `json:"enode"`
	} `json:"result"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
}

func registerBuilder(ctx context.Context, builderAdminApi, beaconApi, rethApi string, input *builderHubRegisterBuilderInput) error {
	builderAdminApi = builderAdminApi + "/api/admin/v1"
	beaconApi = beaconApi + "/eth/v1/node/identity"

	resp, err := http.Get(beaconApi)
	if err != nil {
		return fmt.Errorf("failed to get beacon node identity: %v", err)
	}
	defer resp.Body.Close()

	var identityRespPayload identityResponse
	if err := json.NewDecoder(resp.Body).Decode(&identityRespPayload); err != nil {
		return fmt.Errorf("failed to decode identity resp payload: %v", err)
	}
	peerID := identityRespPayload.Data.PeerID
	libP2PAddr := fmt.Sprintf("/ip4/%s/tcp/9001/p2p/%s", BuilderHostIPAddress, peerID)
	slog.Info("setting builder config var", "libp2p-addr", libP2PAddr)

	respData, err := postRequest(rethApi, "/", map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "admin_nodeInfo",
		"id":      1,
	})
	if err != nil {
		return fmt.Errorf("failed to get reth node info: %v", err)
	}
	var enodeRespPayload enodeResponse
	if err := json.Unmarshal(respData, &enodeRespPayload); err != nil {
		return fmt.Errorf("failed to decode enode resp payload: %v", err)
	}
	if enodeRespPayload.Error.Code != 0 {
		return fmt.Errorf("error from reth admin_nodeInfo: %s", enodeRespPayload.Error.Message)
	}

	// Replace the ip addr with the host ip address known in the builder vm
	bootNode := replaceEnodeIP(enodeRespPayload.Result.Enode, BuilderHostIPAddress)
	slog.Info("setting builder config var", "bootnode", bootNode)

	// Replace template vars.
	input.Config = strings.ReplaceAll(input.Config, "{{EL_BOOTNODE}}", bootNode)
	input.Config = strings.ReplaceAll(input.Config, "{{CL_LIBP2P_ADDR}}", libP2PAddr)

	// Validate input.Config, it must be a valid json file
	var configMap map[string]interface{}
	if err := json.Unmarshal([]byte(input.Config), &configMap); err != nil {
		return err
	}

	// Create Allow-All Measurements
	_, err = postRequest(builderAdminApi, "/measurements", map[string]interface{}{
		"measurement_id":   input.MeasurementID,
		"attestation_type": "test",
		"measurements":     map[string]interface{}{},
	})
	if err != nil {
		return fmt.Errorf("failed to create measurements: %v", err)
	}

	// Enable Measurements
	_, err = postRequest(builderAdminApi, fmt.Sprintf("/measurements/activation/%s", input.MeasurementID), map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		return fmt.Errorf("failed to activate measurements: %v", err)
	}

	// create the builder
	_, err = postRequest(builderAdminApi, "/builders", map[string]interface{}{
		"name":       input.BuilderID,
		"ip_address": input.BuilderIP,
		"network":    input.Network,
	})
	if err != nil {
		return fmt.Errorf("failed to create builder: %v", err)
	}

	// Create Builder Configuration
	_, err = postRequest(builderAdminApi, fmt.Sprintf("/builders/configuration/%s", input.BuilderID), input.Config)
	if err != nil {
		return fmt.Errorf("failed to set builder configuration: %v", err)
	}

	// Create Builder Secrets
	_, err = postRequest(builderAdminApi, fmt.Sprintf("/builders/secrets/%s", input.BuilderID), input.Config)
	if err != nil {
		return fmt.Errorf("failed to set builder secrets: %v", err)
	}

	// Enable Builder
	_, err = postRequest(builderAdminApi, fmt.Sprintf("/builders/activation/%s", input.BuilderID), map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		return fmt.Errorf("failed to activate builder: %v", err)
	}

	return nil
}

func replaceEnodeIP(enodeRaw, vmHostIP string) string {
	re := regexp.MustCompile(`@[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:`)
	return re.ReplaceAllString(enodeRaw, "@"+vmHostIP+":")
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
