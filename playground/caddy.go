package playground

import (
	"fmt"
	"slices"
)

func CreateCaddyServices(exposedServices []string, manifest *Manifest, out *output) error {
	// Create a Caddyfile configuration for reverse proxying all services with HTTP or WS ports
	var routes []string
	manifest.ctx.CaddyEnabled = true

	// Add a routes for each service with http or ws ports
	for _, service := range manifest.services {
		if slices.Contains(exposedServices, service.Name) {
			for _, port := range service.Ports {
				// Only look for HTTP and WebSocket ports
				if port.Name == "http" || port.Name == "ws" {
					// Create a route for the service with port type in the path
					// Format: /<service-name>/<port-type>/* -> http://<service-name>:<port>/{path}
					route := fmt.Sprintf("  handle_path /%s/%s {\n", service.Name, port.Name)
					route += fmt.Sprintf("    uri strip_prefix /%s/%s\n", service.Name, port.Name)
					route += fmt.Sprintf("    reverse_proxy %s:%d\n", service.Name, port.Port)
					route += "  }\n\n"
					routes = append(routes, route)
				}
			}
		}
	}

	if len(routes) == 0 {
		// No HTTP or WS services to proxy, skip creating Caddy
		return fmt.Errorf("no HTTP or WS services to proxy")
	}

	// Create the Caddyfile
	caddyfile := ":8888 {\n"

	// Add all the routes
	for _, route := range routes {
		caddyfile += route
	}

	caddyfile += "}\n"

	// Write the Caddyfile
	if err := out.WriteFile("Caddyfile", caddyfile); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	// Add Caddy service to the manifest
	srv := manifest.NewService("caddy").
		WithImage("caddy").
		WithTag("2").
		WithPort("http", 8888, "tcp").
		WithArtifact("/etc/caddy/Caddyfile", "Caddyfile")

	// Add the service to the manifest
	srv.ComponentName = "null" // Using null since there's no dedicated Caddy component
	manifest.services = append(manifest.services, srv)
	return nil
}
