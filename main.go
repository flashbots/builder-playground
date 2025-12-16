package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/flashbots/builder-playground/playground"
	"github.com/flashbots/builder-playground/utils/mainctx"
	"github.com/spf13/cobra"
)

var version = "dev"

var (
	outputFlag       string
	genesisDelayFlag uint64
	withOverrides    []string
	watchdog         bool
	dryRun           bool
	interactive      bool
	timeout          time.Duration
	logLevelFlag     string
	bindExternal     bool
	withPrometheus   bool
	networkName      string
	labels           playground.MapStringFlag
	disableLogs      bool
	platform         string
	contenderEnabled bool
	contenderArgs    []string
	contenderTarget  string
	detached         bool
)

var rootCmd = &cobra.Command{
	Use:     "playground",
	Short:   "",
	Long:    ``,
	Version: version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var startCmd = &cobra.Command{
	Use:     "start",
	Short:   "Start a recipe",
	Aliases: []string{"cook"},
	RunE: func(cmd *cobra.Command, args []string) error {
		recipeNames := []string{}
		for _, recipe := range recipes {
			recipeNames = append(recipeNames, recipe.Name())
		}
		return fmt.Errorf("please specify a recipe to cook. Available recipes: %s", recipeNames)
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a playground session",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions := args
		if len(sessions) == 0 {
			return fmt.Errorf("please specify at least one session name or 'all' to stop all sessions")
		}
		if len(sessions) == 1 && sessions[0] == "all" {
			var err error
			sessions, err = playground.GetLocalSessions()
			if err != nil {
				return err
			}
		}
		for _, session := range sessions {
			if err := playground.StopSession(session); err != nil {
				return err
			}
			fmt.Println("Stopping session:", session)
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

		ctx := mainctx.Get()

		if err := playground.Inspect(ctx, serviceName, connectionName); err != nil {
			return fmt.Errorf("failed to inspect connection: %w", err)
		}
		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("playground %s\n", version)
	},
}

var recipes = []playground.Recipe{
	&playground.L1Recipe{},
	&playground.OpRecipe{},
	&playground.BuilderNetRecipe{},
}

func main() {
	for _, recipe := range recipes {
		recipeCmd := &cobra.Command{
			Use:   recipe.Name(),
			Short: recipe.Description(),
			RunE: func(cmd *cobra.Command, args []string) error {
				// Silence usage for internal errors, not flag parsing errors
				cmd.SilenceUsage = true
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
		recipeCmd.Flags().StringVar(&networkName, "network", "", "network name")
		recipeCmd.Flags().Var(&labels, "labels", "list of labels to apply to the resources")
		recipeCmd.Flags().BoolVar(&disableLogs, "disable-logs", false, "disable logs")
		recipeCmd.Flags().StringVar(&platform, "platform", "", "docker platform to use")
		recipeCmd.Flags().BoolVar(&contenderEnabled, "contender", false, "spam nodes with contender")
		recipeCmd.Flags().StringArrayVar(&contenderArgs, "contender.arg", []string{}, "add/override contender CLI flags")
		recipeCmd.Flags().StringVar(&contenderTarget, "contender.target", "", "override the node that contender spams -- accepts names like \"el\"")
		recipeCmd.Flags().BoolVar(&detached, "detached", false, "Detached mode: Run the recipes in the background")

		startCmd.AddCommand(recipeCmd)
	}

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(versionCmd)

	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")

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

	exCtx := &playground.ExContext{
		LogLevel: logLevel,
		// if contender.tps is set, assume contender is enabled
		Contender: &playground.ContenderContext{
			Enabled:     contenderEnabled,
			ExtraArgs:   contenderArgs,
			TargetChain: contenderTarget,
		},
	}

	svcManager := playground.NewManifest(exCtx, artifacts.Out)

	recipe.Apply(svcManager)

	// generate the dot graph
	dotGraph := svcManager.GenerateDotGraph()
	if err := artifacts.Out.WriteFile("graph.dot", dotGraph); err != nil {
		return err
	}

	if err := svcManager.Validate(); err != nil {
		return fmt.Errorf("failed to validate manifest: %w", err)
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

	if err := svcManager.ApplyOverrides(overrides); err != nil {
		return err
	}

	cfg := &playground.RunnerConfig{
		Out:                  artifacts.Out,
		Manifest:             svcManager,
		BindHostPortsLocally: !bindExternal,
		NetworkName:          networkName,
		Labels:               labels,
		LogInternally:        !disableLogs,
		Platform:             platform,
	}

	if interactive {
		i := playground.NewInteractiveDisplay(svcManager)
		cfg.Callback = i.HandleUpdate
	}

	// Add callback to log service updates in debug mode
	if logLevel == playground.LevelDebug {
		cfg.Callback = func(serviceName string, update playground.TaskStatus) {
			log.Printf("[DEBUG] [%s] %s\n", serviceName, update)
		}
	}

	dockerRunner, err := playground.NewLocalRunner(cfg)
	if err != nil {
		return fmt.Errorf("failed to create docker runner: %w", err)
	}

	ctx := mainctx.Get()

	if err := dockerRunner.Run(ctx); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("failed to run docker: %w", err)
	}

	if !interactive {
		// print services info
		fmt.Printf("\n========= Services started =========\n")
		for _, ss := range svcManager.Services {
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
	}

	if err := dockerRunner.WaitForReady(ctx, 20*time.Second); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("failed to wait for service readiness: %w", err)
	}

	fmt.Printf("\nWaiting for network to be ready for transactions...\n")
	networkReadyStart := time.Now()
	if err := playground.CompleteReady(ctx, svcManager.Services); err != nil {
		dockerRunner.Stop()
		return fmt.Errorf("network not ready: %w", err)
	}
	fmt.Printf("Network is ready for transactions (took %.1fs)\n", time.Since(networkReadyStart).Seconds())
	fmt.Println("Session ID:", svcManager.ID)

	// get the output from the recipe
	output := recipe.Output(svcManager)
	if len(output) > 0 {
		fmt.Printf("\n========= Output =========\n")
		for k, v := range output {
			fmt.Printf("- %s: %v\n", k, v)
		}
	}

	if detached {
		return nil
	}

	watchdogErr := make(chan error, 1)
	if watchdog {
		go func() {
			if err := playground.RunWatchdog(artifacts.Out, svcManager.Services); err != nil {
				watchdogErr <- fmt.Errorf("watchdog failed: %w", err)
			}
		}()
	}

	var timerCh <-chan time.Time
	if timeout > 0 {
		timerCh = time.After(timeout)
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
