package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/phylaxsystems/builder-playground/playground"
	"github.com/spf13/cobra"
)

var outputFlag string
var genesisDelayFlag uint64
var withOverrides []string
var watchdog bool
var dryRun bool
var interactive bool
var timeout time.Duration
var logLevelFlag string
var bindExternal bool
var withPrometheus bool
var networkName string
var labels playground.MapStringFlag
var disableLogs bool
var withGrafanaAlloy bool
var withCaddy []string
var detach bool

var rootCmd = &cobra.Command{
	Use:   "playground",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var cookCmd = &cobra.Command{
	Use:   "cook",
	Short: "Cook a recipe",
	RunE: func(cmd *cobra.Command, args []string) error {
		recipeNames := []string{}
		for _, recipe := range recipes {
			recipeNames = append(recipeNames, recipe.Name())
		}
		return fmt.Errorf("please specify a recipe to cook. Available recipes: %s", recipeNames)
	},
}

var artifactsCmd = &cobra.Command{
	Use:   "artifacts",
	Short: "List available artifacts",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("please specify a service name")
		}
		serviceName := args[0]
		component := playground.FindComponent(serviceName)
		if component == nil {
			return fmt.Errorf("service %s not found", serviceName)
		}
		releaseService, ok := component.(playground.ReleaseService)
		if !ok {
			return fmt.Errorf("service %s is not a release service", serviceName)
		}
		output := outputFlag
		if output == "" {
			homeDir, err := playground.GetHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			output = homeDir
		}
		location, err := playground.DownloadRelease(output, releaseService.ReleaseArtifact())
		if err != nil {
			return fmt.Errorf("failed to download release: %w", err)
		}
		fmt.Println(location)
		return nil
	},
}

var artifactsAllCmd = &cobra.Command{
	Use:   "artifacts-all",
	Short: "Download all the artifacts available in the catalog (Used for testing purposes)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Downloading all artifacts...")

		output := outputFlag
		if output == "" {
			homeDir, err := playground.GetHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			output = homeDir
		}
		for _, component := range playground.Components {
			releaseService, ok := component.(playground.ReleaseService)
			if !ok {
				continue
			}
			location, err := playground.DownloadRelease(output, releaseService.ReleaseArtifact())
			if err != nil {
				return fmt.Errorf("failed to download release: %w", err)
			}

			// make sure the artifact is valid to be executed on this platform
			log.Printf("Downloaded %s to %s\n", releaseService.ReleaseArtifact().Name, location)
			if err := isExecutableValid(location); err != nil {
				return fmt.Errorf("failed to check if artifact is valid: %w", err)
			}
		}
		return nil
	},
}

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect a connection between two services",
	RunE: func(cmd *cobra.Command, args []string) error {
		// two arguments, the name of the service and the name of the connection
		if len(args) != 2 {
			return fmt.Errorf("please specify a service name and a connection name")
		}
		serviceName := args[0]
		connectionName := args[1]

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-sig
			cancel()
		}()

		if err := playground.Inspect(ctx, serviceName, connectionName); err != nil {
			return fmt.Errorf("failed to inspect connection: %w", err)
		}
		return nil
	},
}

var recipes = []playground.Recipe{
	&playground.L1Recipe{},
	&playground.OpRecipe{},
	&playground.BuilderNetRecipe{},
	&playground.OpTalosRecipe{},
}

func main() {
	for _, recipe := range recipes {
		recipeCmd := &cobra.Command{
			Use:   recipe.Name(),
			Short: recipe.Description(),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runIt(recipe)
			},
		}
		// add the flags from the recipe
		recipeCmd.Flags().AddFlagSet(recipe.Flags())
		// add the common flags
		recipeCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")
		recipeCmd.Flags().BoolVar(&watchdog, "watchdog", false, "enable watchdog")
		recipeCmd.Flags().StringArrayVar(&withOverrides, "override", []string{}, "override a service's config")
		recipeCmd.Flags().BoolVar(&dryRun, "dry-run", false, "dry run the recipe")
		recipeCmd.Flags().BoolVar(&dryRun, "mise-en-place", false, "mise en place mode")
		recipeCmd.Flags().Uint64Var(&genesisDelayFlag, "genesis-delay", playground.MinimumGenesisDelay, "")
		recipeCmd.Flags().BoolVar(&interactive, "interactive", false, "interactive mode")
		recipeCmd.Flags().DurationVar(&timeout, "timeout", 0, "") // Used for CI
		recipeCmd.Flags().StringVar(&logLevelFlag, "log-level", "info", "log level")
		recipeCmd.Flags().BoolVar(&bindExternal, "bind-external", false, "bind host ports to external interface")
		recipeCmd.Flags().BoolVar(&withPrometheus, "with-prometheus", false, "whether to gather the Prometheus metrics")
		recipeCmd.Flags().BoolVar(&withGrafanaAlloy, "with-grafana-alloy", false, "whether to spawn a grafana alloy to agent for metrics, logs, traces")
		recipeCmd.Flags().StringArrayVar(&withCaddy, "with-caddy", []string{}, "Enable caddy and expose the services with the given names")
		recipeCmd.Flags().StringVar(&networkName, "network", "", "network name")
		recipeCmd.Flags().BoolVar(&detach, "detach", false, "detach the services")
		recipeCmd.Flags().Var(&labels, "labels", "list of labels to apply to the resources")
		recipeCmd.Flags().BoolVar(&disableLogs, "disable-logs", false, "disable logs")

		cookCmd.AddCommand(recipeCmd)
	}

	// reuse the same output flag for the artifacts command
	artifactsCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")
	artifactsAllCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")

	rootCmd.AddCommand(cookCmd)
	rootCmd.AddCommand(artifactsCmd)
	rootCmd.AddCommand(artifactsAllCmd)
	rootCmd.AddCommand(inspectCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runIt(recipe playground.Recipe) error {
	var logLevel playground.LogLevel
	if err := logLevel.Unmarshal(logLevelFlag); err != nil {
		return fmt.Errorf("failed to parse log level: %w", err)
	}

	log.Printf("Log level: %s\n", logLevel)

	// parse the overrides
	overrides := map[string]string{}
	for _, val := range withOverrides {
		parts := strings.SplitN(val, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid override format: %s, expected service=val", val)
		}
		overrides[parts[0]] = parts[1]
	}

	builder := recipe.Artifacts()
	builder.OutputDir(outputFlag)
	builder.GenesisDelay(genesisDelayFlag)
	artifacts, err := builder.Build()
	if err != nil {
		return err
	}

	svcManager, err := recipe.Apply(&playground.ExContext{LogLevel: logLevel, AlloyEnabled: withGrafanaAlloy, CaddyEnabled: len(withCaddy) > 0}, artifacts)
	if err != nil {
		return fmt.Errorf("failed to apply recipe: %w", err)
	}

	if withGrafanaAlloy {
		if err := playground.CreateGrafanaAlloyServices(svcManager, artifacts.Out); err != nil {
			return fmt.Errorf("failed to create grafana alloy services: %w", err)
		}
	}

	if len(withCaddy) > 0 {
		log.Printf("Spawning a caddy reverse proxy for the services: %v\n", withCaddy)
		if err := playground.CreateCaddyServices(withCaddy, svcManager, artifacts.Out); err != nil {
			return fmt.Errorf("failed to create caddy services: %w", err)
		}
	}

	if err := svcManager.Validate(); err != nil {
		return fmt.Errorf("failed to validate manifest: %w", err)
	}

	// generate the dot graph
	dotGraph := svcManager.GenerateDotGraph()
	if err := artifacts.Out.WriteFile("graph.dot", dotGraph); err != nil {
		return err
	}

	// save the manifest.json file
	if err := svcManager.SaveJson(); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	if withPrometheus {
		if err := playground.CreatePrometheusServices(svcManager, artifacts.Out); err != nil {
			return fmt.Errorf("failed to create prometheus services: %w", err)
		}
	}

	if dryRun {
		return nil
	}

	// validate that override is being applied to a service in the manifest
	for k := range overrides {
		if _, ok := svcManager.GetService(k); !ok {
			return fmt.Errorf("service '%s' in override not found in manifest", k)
		}
	}

	dockerRunner, err := playground.NewLocalRunner(artifacts.Out, svcManager, overrides, interactive, !bindExternal, networkName, labels, !disableLogs)
	if err != nil {
		return fmt.Errorf("failed to create docker runner: %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-sig
		cancel()
	}()

	if err := dockerRunner.Run(); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("failed to run docker: %w", err)
	}

	if !interactive {
		// print services info
		fmt.Printf("\n========= Services started =========\n")
		for _, ss := range svcManager.Services() {
			ports := ss.GetPorts()
			sort.Slice(ports, func(i, j int) bool {
				return ports[i].Name < ports[j].Name
			})

			portsStr := []string{}
			for _, p := range ports {
				protocol := ""
				if p.Protocol == playground.ProtocolUDP {
					protocol = "/udp"
				}
				portsStr = append(portsStr, fmt.Sprintf("%s: %d/%d%s", p.Name, p.Port, p.HostPort, protocol))
			}
			fmt.Printf("- %s (%s)\n", ss.Name, strings.Join(portsStr, ", "))
		}
		if len(withCaddy) > 0 {
			fmt.Printf("\n========= Caddy Exposed Services =========\n")
			for _, serviceName := range withCaddy {
				service, ok := svcManager.GetService(serviceName)
				if ok {
					for _, port := range service.GetPorts() {
						if port.Name == "http" || port.Name == "ws" {
							fmt.Printf("- %s (%s): http://localhost:8888/%s/%s\n",
								service.Name, port.Name, service.Name, port.Name)
						}
					}
				}
			}
		}
	}

	if err := dockerRunner.WaitForReady(ctx, 20*time.Second); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("failed to wait for service readiness: %w", err)
	}

	if err := playground.CompleteReady(dockerRunner.Instances()); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("failed to complete ready: %w", err)
	}

	// get the output from the recipe
	output := recipe.Output(svcManager)
	if len(output) > 0 {
		fmt.Printf("\n========= Output =========\n")
		for k, v := range output {
			fmt.Printf("- %s: %v\n", k, v)
		}
	}

	watchdogErr := make(chan error, 1)
	if watchdog {
		go func() {
			if err := playground.RunWatchdog(artifacts.Out, dockerRunner.Instances()); err != nil {
				watchdogErr <- fmt.Errorf("watchdog failed: %w", err)
			}
		}()
	}

	var timerCh <-chan time.Time
	if timeout > 0 {
		timerCh = time.After(timeout)
	}

	if detach {
		return nil
	}

	select {
	case <-ctx.Done():
		fmt.Println("Stopping...")
	case err := <-dockerRunner.ExitErr():
		fmt.Println("Service failed:", err)
	case err := <-watchdogErr:
		fmt.Println("Watchdog failed:", err)
	case <-timerCh:
		fmt.Println("Timeout reached")
	}

	if err := dockerRunner.Stop(); err != nil {
		return fmt.Errorf("failed to stop docker: %w", err)
	}
	return nil
}

func isExecutableValid(path string) error {
	// First check if file exists
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file does not exist or is inaccessible: %w", err)
	}

	// Try to execute with a harmless flag or in a way that won't run the main program
	cmd := exec.Command(path, "--version")
	// Redirect output to /dev/null
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start executable: %w", err)
	}

	// Immediately kill the process since we just want to test if it starts
	cmd.Process.Kill()

	return nil
}
