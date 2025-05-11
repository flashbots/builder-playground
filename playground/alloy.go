package playground

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// CreateGrafanaAlloyServices creates a service running Grafana Alloy for observability
// that collects metrics, logs, and traces from all services.
func CreateGrafanaAlloyServices(manifest *Manifest, out *output) error {
	// Try to load environment variables from .env.grafana file
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}
	envFilePath := filepath.Join(cwd, ".env.grafana")

	// Load .env.grafana file if it exists (silently continues if file doesn't exist)
	_ = godotenv.Load(envFilePath)

	// Check for required environment variables
	requiredEnvVars := []string{
		"GRAFANA_METRICS_URL",
		"GRAFANA_METRICS_USERNAME",
		"GRAFANA_LOGS_URL",
		"GRAFANA_LOGS_USERNAME",
		"GRAFANA_TRACES_URL",
		"GRAFANA_TRACES_USERNAME",
		"GRAFANA_CLOUD_API_KEY",
	}

	// Check if all required environment variables are set
	missingVars := []string{}
	for _, envVar := range requiredEnvVars {
		if os.Getenv(envVar) == "" {
			missingVars = append(missingVars, envVar)
		}
	}

	if len(missingVars) > 0 {
		// Create a helpful error message with instructions
		errorMsg := fmt.Sprintf("Missing required environment variables for Grafana Alloy: %s\n\n",
			strings.Join(missingVars, ", "))
		errorMsg += "Please either:\n"
		errorMsg += "1. Set these environment variables in your shell, or\n"
		errorMsg += fmt.Sprintf("2. Create a .env.grafana file in %s with these variables in KEY=VALUE format\n", out.dst)
		return fmt.Errorf(errorMsg)
	}

	// Create the Grafana Alloy configuration
	alloyConfig := `
// Metrics

prometheus.remote_write "metrics_service" {
	endpoint {
		url = sys.env("GRAFANA_METRICS_URL")

		basic_auth {
			username = sys.env("GRAFANA_METRICS_USERNAME")
			password = sys.env("GRAFANA_CLOUD_API_KEY")
		}
	}
}

prometheus.exporter.cadvisor "containers" {
  docker_host = "unix:///var/run/docker.sock"
  storage_duration = "5m"
}

prometheus.scrape "containers" {
  targets    = prometheus.exporter.cadvisor.containers.targets
  forward_to = [prometheus.remote_write.metrics_service.receiver]
  scrape_interval = "10s"
}


// Scrape metrics from services
prometheus.scrape "playground" {
  targets = [
`
	// Add a scrape target for each service that exposes metrics
	scrapeTargets := []string{}
	for _, service := range manifest.services {
		for _, port := range service.Ports {
			if port.Name == "metrics" {
				metricsPath := "/metrics"
				if overrideMetricsPath, ok := service.Labels["metrics_path"]; ok {
					metricsPath = overrideMetricsPath
				}

				// Create a scrape target for the service
				scrapeTarget := fmt.Sprintf("    // %s\n    {__address__ = \"%s:%d\", __metrics_path__ = \"%s\"},",
					service.Name, service.Name, port.Port, metricsPath)
				scrapeTargets = append(scrapeTargets, scrapeTarget)
			}
		}
	}

	// If no service exposes metrics, add a default target
	if len(scrapeTargets) == 0 {
		scrapeTargets = append(scrapeTargets, "    // Default\n    {__address__ = \"localhost:5555\"}")
	}

	// Add the targets to the alloy config - ensure proper comma formatting
	alloyConfig += strings.Join(scrapeTargets, "") + "\n"

	// Continue the alloy config
	alloyConfig += `  ]
  forward_to = [prometheus.remote_write.metrics_service.receiver]
  scrape_interval = "10s"  // cannot be lower than poll_frequency
  scrape_timeout = "1s"  // should be lower than scrape_interval
}

// Add a scrape job for Grafana Alloy's own metrics
prometheus.scrape "alloy_self" {
  targets = [
    {__address__ = "localhost:4000", __metrics_path__ = "/metrics"},
  ]
  forward_to = [prometheus.remote_write.metrics_service.receiver]
  scrape_interval = "10s"
  scrape_timeout = "1s"
}

// Traces
otelcol.receiver.otlp "otlp_receiver" {
  grpc {
    endpoint = "0.0.0.0:4317"
  }
  http {
    endpoint = "0.0.0.0:4318"
  }

  output {
    traces = [otelcol.processor.resourcedetection.container_detector.input]
  }
}

// Resource detection processor according to Grafana docs
otelcol.processor.resourcedetection "container_detector" {
  // Use environment variables and docker metadata for detection
  detectors = ["env", "docker" ]
  // Set appropriate timeout
  timeout = "5s"

  output {
    traces = [otelcol.exporter.otlphttp.grafanacloud.input]
  }
}

// Export traces
otelcol.exporter.otlphttp "grafanacloud" {
  client {
    endpoint = sys.env("GRAFANA_TRACES_URL")
    auth = otelcol.auth.basic.grafanacloud.handler
  }
}

otelcol.auth.basic "grafanacloud" {
  username = sys.env("GRAFANA_TRACES_USERNAME")
  password = sys.env("GRAFANA_CLOUD_API_KEY")
}


// Collect logs from Docker containers
discovery.docker "linux" {
  host = "unix:///var/run/docker.sock"
}


discovery.relabel "logs_integrations_docker" {
  targets = []

  rule {
    source_labels = ["__meta_docker_container_name"]
    regex = "/(.*)"
    target_label = "service_name"
  }
  rule {
	target_label = "instance"
	replacement = constants.hostname
  }
}


loki.source.docker "default" {
  host       = "unix:///var/run/docker.sock"
  targets    = discovery.docker.linux.targets
  labels     = {"platform" = "docker"}
  relabel_rules = discovery.relabel.logs_integrations_docker.rules
  forward_to = [loki.write.grafanacloud.receiver]
  refresh_interval = "5s"
}

// Logs
loki.write "grafanacloud" {
	endpoint {
		url = sys.env("GRAFANA_LOGS_URL")

		basic_auth {
			username = sys.env("GRAFANA_LOGS_USERNAME")
			password = sys.env("GRAFANA_CLOUD_API_KEY")
		}
	}
}

`

	// Write the alloy configuration file
	if err := out.WriteFile("alloy.river", alloyConfig); err != nil {
		return fmt.Errorf("failed to write alloy.river: %w", err)
	}

	// Add Grafana Alloy service to the manifest
	srv := manifest.NewService("grafana-alloy").
		WithImage("grafana/alloy").
		WithTag("latest").
		WithArgs(
			"run",
			"/etc/alloy/alloy.river",
			"--server.http.listen-addr",
			`0.0.0.0:{{Port "http" 12345}}`,
		).
		// Metrics port
		WithPort("metrics", 4000, "tcp").
		// OTLP gRPC port
		WithPort("otlp-grpc", 4317, "tcp").
		// OTLP HTTP port
		WithPort("otlp-http", 4318, "tcp").
		// Mount the alloy config file
		WithArtifact("/etc/alloy/alloy.river", "alloy.river").
		// Mount Docker socket for container discovery
		// and other cadvisor metrics
		// TODO: Add a new method to add 'ro' volumes.
		// Instead of adding all of them as 'rw'
		WithAbsoluteVolume("/rootfs", "/").
		WithAbsoluteVolume("/var/run", "/var/run").
		WithAbsoluteVolume("/var/run/docker.sock", "/var/run/docker.sock").
		WithAbsoluteVolume("/sys", "/sys").
		WithAbsoluteVolume("/var/lib/docker", "/var/lib/docker").
		WithAbsoluteVolume("/dev/disk", "/dev/disk").
		WithPrivileged()

	// Add environment variables with values from environment
	for _, envVar := range requiredEnvVars {
		srv.WithEnv(envVar, os.Getenv(envVar))
	}

	srv.ComponentName = "null" // For now, later on we can create a Grafana Alloy component
	manifest.services = append(manifest.services, srv)

	return nil
}
