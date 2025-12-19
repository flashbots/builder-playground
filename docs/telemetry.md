
# Telemetry

The Builder Playground includes built-in Prometheus metrics collection. When you run any recipe with the `--with-prometheus` flag, the system automatically deploys a Prometheus server and gathers metrics from all services in your deployment.

Prometheus automatically discovers services by looking for a port with the metrics label. You can define a metrics port in your component like this:

```go
WithArgs("--metrics", `0.0.0.0:{{Port "metrics" 9090}}`)
```

By default, Prometheus scrapes the `/metrics` path, but services can override this by specifying a custom path with `WithLabel("metrics_path", "/custom/path")`. All configured services are automatically registered as scrape targets.

## Usage
Enable Prometheus for any recipe:

```bash
$ builder-playground start l1 --with-prometheus
$ builder-playground start opstack --with-prometheus
```
