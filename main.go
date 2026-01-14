package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/flashbots/builder-playground/playground"
	"github.com/flashbots/builder-playground/utils"
	"github.com/flashbots/builder-playground/utils/logging"
	"github.com/flashbots/builder-playground/utils/mainctx"
	"github.com/spf13/cobra"
)

var version = "dev"

var greenColor = color.New(color.FgGreen)

var (
	keepFlag          bool
	outputFlag        string
	genesisDelayFlag  uint64
	withOverrides     []string
	watchdog          bool
	dryRun            bool
	interactive       bool
	timeout           time.Duration
	logLevelFlag      string
	bindExternal      bool
	withPrometheus    bool
	networkName       string
	labels            playground.MapStringFlag
	disableLogs       bool
	platform          string
	contenderEnabled  bool
	contenderArgs     []string
	contenderTarget   string
	detached          bool
	prefundedAccounts []string
	noFollow          bool
)

var rootCmd = &cobra.Command{
	Use:     "playground",
	Short:   "",
	Long:    ``,
	Version: version,
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
	Short: "Stop a playground session or all sessions",
	RunE:  shutDownCmdFunc("stop"),
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean up a playground session or all sessions",
	RunE:  shutDownCmdFunc("clean"),
}

func shutDownCmdFunc(cmdName string) func(cmd *cobra.Command, args []string) error {
	var keepResources bool
	switch cmdName {
	case "stop":
		keepResources = true

	case "clean":

	default:
		panic("setting up shut down func for unknown cmd: " + cmdName)
	}
	return func(cmd *cobra.Command, args []string) error {
		sessions := args
		if len(sessions) == 0 {
			return fmt.Errorf("please specify at least one session name or 'all' to %s all sessions", cmdName)
		}
		if len(sessions) == 1 && sessions[0] == "all" {
			var err error
			sessions, err = playground.GetLocalSessions()
			if err != nil {
				return err
			}
		}
		for _, session := range sessions {
			fmt.Printf("%s: %s\n", cmdName, session)
			if err := playground.StopSession(session, keepResources); err != nil {
				return err
			}
		}
		return nil
	}
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

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debug commands for running services",
}

var probeCmd = &cobra.Command{
	Use:   "probe <service>",
	Short: "Execute a service's health check manually",
	Long:  "Manually runs the configured Docker health check command for a service and displays the result.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		serviceName := args[0]

		resp, err := playground.ExecuteHealthCheckManually(serviceName)
		if err != nil {
			return err
		}

		fmt.Printf("Exit code: %d\n", resp.ExitCode)
		fmt.Printf("Output: %s\n", resp.Output)

		return nil
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs for a service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := mainctx.Get()
		follow := !noFollow

		switch len(args) {
		case 1:
			sessions, err := playground.GetLocalSessions()
			if err != nil {
				return err
			}
			if len(sessions) > 1 {
				cmd.SilenceUsage = true
				fmt.Println("multiple sessions found: please use 'list' to see all and provide like 'logs <session-name> <service-name>'")
				return fmt.Errorf("invalid amount of args")
			} else {
				if err := playground.Logs(ctx, "", args[0], follow); err != nil && !strings.Contains(err.Error(), "signal") {
					return fmt.Errorf("failed to show logs: %w", err)
				}
			}
		case 2:
			if err := playground.Logs(ctx, args[0], args[1], follow); err != nil && !strings.Contains(err.Error(), "signal") {
				return fmt.Errorf("failed to show logs: %w", err)
			}

		default:
			cmd.SilenceUsage = true
			fmt.Println("either specify '<service-name>' or '<session-name> <service-name>'")
			fmt.Println("if single session is running: 'builder-playground logs beacon'")
			fmt.Println("with multiple sessions: 'builder-playground logs major-hornet beacon'")
			return fmt.Errorf("invalid amount of args")
		}

		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sessions and running services",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions, err := playground.GetLocalSessions()
		if err != nil {
			return err
		}

		// If running single session, just list the services of that without expecting a session name.
		if len(sessions) == 1 {
			services, err := playground.GetSessionServices(sessions[0])
			if err != nil {
				return err
			}
			fmt.Println("session: " + sessions[0])
			fmt.Println("--------")
			for _, service := range services {
				fmt.Println(service)
			}
			return nil
		}

		switch len(args) {
		case 0:
			fmt.Println("sessions:")
			fmt.Println("---------")
			for _, session := range sessions {
				fmt.Println(session)
			}

		case 1:
			services, err := playground.GetSessionServices(args[0])
			if err != nil {
				return err
			}
			fmt.Println("session: " + args[0])
			fmt.Println("--------")
			for _, service := range services {
				fmt.Println(service)
			}

		default:
			cmd.SilenceUsage = true // silence usage this time so that the below message is visible
			fmt.Println("please use 'list' to see sessions and 'list <session>' to see containers")
			return fmt.Errorf("invalid amount of args")
		}
		return nil
	},
}

var generateDocsCmd = &cobra.Command{
	Use:   "generate-docs",
	Short: "Generate documentation for all recipes",
	RunE: func(cmd *cobra.Command, args []string) error {
		return playground.GenerateDocs(recipes)
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
		recipeCmd.Flags().BoolVar(&keepFlag, "keep", false, "keep the containers and resources after the session is stopped")
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
		recipeCmd.Flags().StringArrayVar(&prefundedAccounts, "prefunded-accounts", []string{}, "Fund this account in addition to static prefunded accounts, the input should the account's private key in hexadecimal format prefixed with 0x, the account is added to L1 and to L2 (if present)")

		startCmd.AddCommand(recipeCmd)
	}

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(inspectCmd)

	logsCmd.Flags().BoolVar(&noFollow, "no-follow", false, "Display all logs and exit instead of streaming")
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(cleanCmd)

	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")

	debugCmd.AddCommand(probeCmd)
	debugCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(generateDocsCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runIt(recipe playground.Recipe) error {
	fmt.Println()

	var logLevel playground.LogLevel
	if err := logLevel.Unmarshal(logLevelFlag); err != nil {
		return fmt.Errorf("failed to parse log level: %w", err)
	}
	logging.ConfigureSlog(logLevelFlag)
	sessionID := utils.GeneratePetName()
	slog.Info("Welcome to Builder Playground! ⚡️🤖")
	slog.Info("Session ID: "+greenColor.Sprint(sessionID), "log-level", logLevel)
	slog.Info("")

	// parse the overrides
	overrides := map[string]string{}
	for _, val := range withOverrides {
		parts := strings.SplitN(val, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid override format: %s, expected service=val", val)
		}
		overrides[parts[0]] = parts[1]
	}

	out, err := playground.NewOutput(outputFlag)
	if err != nil {
		return err
	}

	slog.Debug("Building artifacts...")
	builder := recipe.Artifacts()
	builder.GenesisDelay(genesisDelayFlag)
	builder.PrefundedAccounts(prefundedAccounts)
	if err := builder.Build(out); err != nil {
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

	svcManager := playground.NewManifest(exCtx, out, sessionID)

	recipe.Apply(svcManager)

	// generate the dot graph
	slog.Debug("Generating dot graph...")
	dotGraph := svcManager.GenerateDotGraph()
	if err := out.WriteFile("graph.dot", dotGraph); err != nil {
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
		if err := playground.CreatePrometheusServices(svcManager, out); err != nil {
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
		Out:                  out,
		Manifest:             svcManager,
		BindHostPortsLocally: !bindExternal,
		NetworkName:          networkName,
		Labels:               labels,
		LogInternally:        !disableLogs,
		Platform:             platform,
	}

	if interactive {
		i := playground.NewInteractiveDisplay(svcManager)
		cfg.AddCallback(i.HandleUpdate)
	}

	// Add callback to log service updates in debug mode
	cfg.AddCallback(func(serviceName string, update playground.TaskStatus) {
		slog.Debug("service update", "service", serviceName, "status", update)
	})

	dockerRunner, err := playground.NewLocalRunner(cfg)
	if err != nil {
		return fmt.Errorf("failed to create docker runner: %w", err)
	}

	ctx := mainctx.Get()

	slog.Info("Starting services... ⏳", "session-id", svcManager.ID)
	if err := dockerRunner.Run(ctx); err != nil {
		dockerRunner.Stop(keepFlag)
		return fmt.Errorf("failed to run docker: %w", err)
	}

	if !interactive {
		log.Println()
		log.Println("All services started! ✅")
		// print services info
		for _, ss := range svcManager.Services {
			ports := ss.GetPorts()
			sort.Slice(ports, func(i, j int) bool {
				return ports[i].Name < ports[j].Name
			})

			var svcInfo []any
			svcInfo = append(svcInfo, "image", ss.Image, "tag", ss.Tag)
			for _, p := range ports {
				protocol := ""
				if p.Protocol == playground.ProtocolUDP {
					protocol = "/udp"
				}
				svcInfo = append(svcInfo, p.Name, fmt.Sprintf("%d/%d%s", p.Port, p.HostPort, protocol))
			}
			slog.Info("• "+ss.Name, svcInfo...)
		}
		log.Println()
	}

	log.Println("Waiting for services to get healthy... ⏳")
	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := dockerRunner.WaitForReady(waitCtx); err != nil {
		dockerRunner.Stop(keepFlag)
		return fmt.Errorf("failed to wait for service readiness: %w", err)
	}

	// run post hook operations
	if err := svcManager.ExecutePostHookActions(); err != nil {
		dockerRunner.Stop(keepFlag)
		return fmt.Errorf("failed to execute post-hook operations: %w", err)
	}

	slog.Info("All services are healthy! Ready to accept transactions. 🚀", "session-id", svcManager.ID)

	// get the output from the recipe
	output := recipe.Output(svcManager)
	if len(output) > 0 {
		slog.Info("")
		slog.Info("Recipe outputs 🔍")
		for k, v := range output {
			valStr := fmt.Sprintf("%v", v)
			slog.Info("• " + k + ": " + greenColor.Sprint(valStr))
		}
	}

	if detached {
		return nil
	}

	watchdogErr := make(chan error, 1)
	if watchdog {
		go func() {
			cfg.AddCallback(func(name string, status playground.TaskStatus) {
				if status == playground.TaskStatusUnhealty {
					watchdogErr <- fmt.Errorf("watchdog failed: %w", fmt.Errorf("task '%s' is not healthy anymore", name))
				}
			})
		}()
	}

	var timerCh <-chan time.Time
	if timeout > 0 {
		timerCh = time.After(timeout)
	}

	select {
	case <-ctx.Done():
		log.Println("Stopping...")
	case err := <-dockerRunner.ExitErr():
		log.Println("Service failed:", err)
	case err := <-watchdogErr:
		log.Println("Watchdog failed:", err)
	case <-timerCh:
		log.Println("Timeout reached")
	}

	if err := dockerRunner.Stop(keepFlag); err != nil {
		return fmt.Errorf("failed to stop docker: %w", err)
	}
	return nil
}
