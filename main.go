package main

import (
	"context"
	"embed"
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

//go:embed custom-recipes/*
var customRecipesFS embed.FS

var version = "dev"

var (
	greenColor         = color.New(color.FgGreen)
	whiteTitleColor    = color.New(color.FgHiWhite, color.Bold)
	recipesColor       = color.RGB(0, 206, 209)   // Dark Turquoise
	customRecipesColor = color.RGB(255, 127, 80)  // Coral
	descriptionColor   = color.RGB(169, 169, 169) // Faded gray, bold for descriptions
	componentsColor    = color.RGB(128, 128, 128) // Faded gray for components
)

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
	followFlag        bool
	generateForce     bool
	testRPCURL        string
	testELRPCURL      string
	testTimeout       time.Duration
	portListFlag      bool
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
		// Check if the first argument is a YAML recipe file
		if len(args) > 0 && playground.IsYAMLRecipeFile(args[0]) {
			yamlRecipe, err := playground.ParseYAMLRecipe(args[0], recipes)
			if err != nil {
				return fmt.Errorf("failed to parse YAML recipe: %w", err)
			}
			cmd.SilenceUsage = true
			return runIt(yamlRecipe)
		}

		// Check if the first argument is a custom recipe name
		if len(args) > 0 {
			customRecipes, err := playground.GetEmbeddedCustomRecipes()
			if err == nil {
				for _, cr := range customRecipes {
					if cr == args[0] {
						yamlRecipe, cleanup, err := playground.LoadCustomRecipe(args[0], recipes)
						if err != nil {
							return err
						}
						defer cleanup()
						cmd.SilenceUsage = true
						return runIt(yamlRecipe)
					}
				}
			}
		}

		recipeNames := []string{}
		for _, recipe := range recipes {
			recipeNames = append(recipeNames, recipe.Name())
		}
		return fmt.Errorf("please specify a recipe to cook. Available recipes: %s, or provide a YAML recipe file path", recipeNames)
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

var validateCmd = &cobra.Command{
	Use:   "validate <recipe>",
	Short: "Validate a recipe without starting it",
	Long:  "Validates a recipe's configuration, checking for issues like missing dependencies, invalid host paths, and configuration errors.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipeName := args[0]

		// Check if it's a YAML recipe file
		if playground.IsYAMLRecipeFile(recipeName) {
			yamlRecipe, err := playground.ParseYAMLRecipe(recipeName, recipes)
			if err != nil {
				return fmt.Errorf("failed to parse YAML recipe: %w", err)
			}
			return runValidation(yamlRecipe)
		}

		// Check base recipes
		for _, recipe := range recipes {
			if recipe.Name() == recipeName {
				return runValidation(recipe)
			}
		}

		return fmt.Errorf("recipe '%s' not found", recipeName)
	},
}

func runValidation(recipe playground.Recipe) error {
	fmt.Printf("Validating recipe: %s\n\n", recipe.Name())

	result := playground.ValidateRecipe(recipe, recipes)

	if len(result.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  - %s\n", w)
		}
		fmt.Println()
	}

	if len(result.Errors) > 0 {
		fmt.Println("Errors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %s\n", e)
		}
		fmt.Println()
		return fmt.Errorf("validation failed with %d error(s)", len(result.Errors))
	}

	fmt.Println("Validation passed!")
	return nil
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
		follow := followFlag

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
			}
			// Use the session name if exactly one exists
			sessionName := ""
			if len(sessions) == 1 {
				sessionName = sessions[0]
			}
			if err := playground.Logs(ctx, sessionName, args[0], follow); err != nil && !strings.Contains(err.Error(), "signal") {
				return fmt.Errorf("failed to show logs: %w", err)
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

var portCmd = &cobra.Command{
	Use:   "port [session] <service> <port-name>",
	Short: "Get the host port for a service",
	Long: `Get the host port for a service's named port.

If only one session is running, the session name can be omitted:
  playground port <service> <port-name>

With multiple sessions, specify the session:
  playground port <session> <service> <port-name>

Use --list to show all available ports for a service:
  playground port <service> --list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var session, serviceName, portName string

		if portListFlag {
			switch len(args) {
			case 1:
				serviceName = args[0]
			case 2:
				session = args[0]
				serviceName = args[1]
			default:
				return fmt.Errorf("expected 1 or 2 arguments with --list")
			}
		} else {
			switch len(args) {
			case 2:
				serviceName = args[0]
				portName = args[1]
			case 3:
				session = args[0]
				serviceName = args[1]
				portName = args[2]
			default:
				return fmt.Errorf("expected 2 or 3 arguments")
			}
		}

		cmd.SilenceUsage = true

		result, err := playground.GetServicePort(session, serviceName, portName)
		if err != nil {
			return err
		}

		fmt.Println(result)
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

var generateCmd = &cobra.Command{
	Use:   "generate <recipe>",
	Short: "Generate a playground.yaml file from a recipe (e.g. l1) or custom recipe (e.g. rbuilder/release)",
	Long:  "Generate a playground.yaml file that represents the full configuration of a recipe",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("please specify a recipe or custom recipe. Run 'playground recipes' to see available options")
		}

		name := args[0]

		// First check if it's a recipe
		for _, r := range recipes {
			if r.Name() == name {
				yamlContent, err := playground.RecipeToYAML(r)
				if err != nil {
					return fmt.Errorf("failed to convert recipe to YAML: %w", err)
				}
				outFile := "playground.yaml"
				if !generateForce {
					if _, err := os.Stat(outFile); err == nil {
						return fmt.Errorf("file %s already exists. Use --force to overwrite", outFile)
					}
				}
				if err := os.WriteFile(outFile, []byte(yamlContent), 0o644); err != nil {
					return fmt.Errorf("failed to write %s: %w", outFile, err)
				}
				fmt.Printf("Created %s\n", outFile)
				return nil
			}
		}

		// Check if it's a custom recipe
		customRecipes, err := playground.GetEmbeddedCustomRecipes()
		if err != nil {
			return fmt.Errorf("failed to list custom recipes: %w", err)
		}
		for _, cr := range customRecipes {
			if cr == name {
				return playground.GenerateFromCustomRecipe(name, generateForce)
			}
		}

		return fmt.Errorf("recipe or custom recipe '%s' not found. Run 'playground recipes' to see available options", name)
	},
}

var recipesCmd = &cobra.Command{
	Use:   "recipes",
	Short: "List all available recipes and custom recipes",
	RunE: func(cmd *cobra.Command, args []string) error {
		customRecipes, err := playground.GetEmbeddedCustomRecipes()
		if err != nil {
			return fmt.Errorf("failed to list custom recipes: %w", err)
		}
		whiteTitleColor.Println("Base Recipes:")
		for _, recipe := range recipes {
			fmt.Println("  " + recipesColor.Sprint(recipe.Name()))
			descriptionColor.Add(color.Bold).Printf("    %s\n", recipe.Description())
			componentsStr := playground.GetRecipeComponentsFormatted(recipe)
			if componentsStr != "" {
				componentsColor.Printf("    %s\n", componentsStr)
			}
			fmt.Println()
		}
		whiteTitleColor.Println("Custom Recipes:")
		for _, cr := range customRecipes {
			fmt.Println("  " + customRecipesColor.Sprint(cr))
			info, err := playground.GetCustomRecipeInfo(cr, recipes)
			if err == nil {
				if info.Description != "" {
					descriptionColor.Add(color.Bold).Printf("    %s\n", info.Description)
				}
				// Show base + modified/new components
				var parts []string
				parts = append(parts, info.Base)
				if len(info.ModifiedComponents) > 0 {
					parts = append(parts, "modified "+strings.Join(info.ModifiedComponents, ", "))
				}
				if len(info.NewComponents) > 0 {
					parts = append(parts, "new "+strings.Join(info.NewComponents, ", "))
				}
				if len(parts) > 1 {
					componentsColor.Printf("    %s\n", strings.Join(parts, " + "))
				}
			}
			fmt.Println()
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

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a test transaction to the local EL node",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		ctx := mainctx.Get()
		cfg := playground.DefaultTestTxConfig()
		cfg.RPCURL = testRPCURL
		cfg.ELRPCURL = testELRPCURL
		cfg.Timeout = testTimeout
		return playground.SendTestTransaction(ctx, cfg)
	},
}

var recipes = []playground.Recipe{
	&playground.L1Recipe{},
	&playground.OpRecipe{},
	&playground.BuilderNetRecipe{},
}

func main() {
	// Set the embedded custom recipes filesystem for the playground package
	playground.CustomRecipesFS = customRecipesFS

	// Helper to add common recipe flags to a command
	addCommonRecipeFlags := func(cmd *cobra.Command) {
		cmd.Flags().BoolVar(&keepFlag, "keep", false, "keep the containers and resources after the session is stopped")
		cmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")
		cmd.Flags().BoolVar(&watchdog, "watchdog", false, "enable watchdog")
		cmd.Flags().StringArrayVar(&withOverrides, "override", []string{}, "override a service's config")
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "dry run the recipe")
		cmd.Flags().BoolVar(&dryRun, "mise-en-place", false, "mise en place mode")
		cmd.Flags().Uint64Var(&genesisDelayFlag, "genesis-delay", playground.MinimumGenesisDelay, "")
		cmd.Flags().BoolVar(&interactive, "interactive", false, "interactive mode")
		cmd.Flags().DurationVar(&timeout, "timeout", 0, "")
		cmd.Flags().StringVar(&logLevelFlag, "log-level", "info", "log level")
		cmd.Flags().BoolVar(&bindExternal, "bind-external", false, "bind host ports to external interface")
		cmd.Flags().BoolVar(&withPrometheus, "with-prometheus", false, "whether to gather the Prometheus metrics")
		cmd.Flags().StringVar(&networkName, "network", "", "network name")
		cmd.Flags().Var(&labels, "labels", "list of labels to apply to the resources")
		cmd.Flags().BoolVar(&disableLogs, "disable-logs", false, "disable logs")
		cmd.Flags().StringVar(&platform, "platform", "", "docker platform to use")
		cmd.Flags().BoolVar(&contenderEnabled, "contender", false, "spam nodes with contender")
		cmd.Flags().StringArrayVar(&contenderArgs, "contender.arg", []string{}, "add/override contender CLI flags")
		cmd.Flags().StringVar(&contenderTarget, "contender.target", "", "override the node that contender spams")
		cmd.Flags().BoolVar(&detached, "detached", false, "Detached mode: Run the recipes in the background")
		cmd.Flags().StringArrayVar(&prefundedAccounts, "prefunded-accounts", []string{}, "Fund this account in addition to static prefunded accounts")
	}

	// Add common flags to startCmd for YAML recipe files
	addCommonRecipeFlags(startCmd)

	// Load custom recipes first to get their flags and descriptions
	customRecipeNames, _ := playground.GetEmbeddedCustomRecipes()
	customRecipeDisplayNames := make(map[playground.Recipe]string)
	var customRecipes []playground.Recipe

	for _, crName := range customRecipeNames {
		customRecipe, cleanup, err := playground.LoadCustomRecipe(crName, recipes)
		if err != nil {
			continue // Skip invalid custom recipes
		}
		customRecipes = append(customRecipes, customRecipe)
		customRecipeDisplayNames[customRecipe] = crName
		cleanup() // Clean up temp dir from flag discovery
	}

	// Register all recipes (built-in and custom) as subcommands
	for _, recipe := range append(recipes, customRecipes...) {
		recipe := recipe // capture loop variable
		recipeName := recipe.Name()
		customRecipeName, isCustom := customRecipeDisplayNames[recipe]
		if isCustom {
			recipeName = customRecipeName
		}

		recipeCmd := &cobra.Command{
			Use:   recipeName,
			Short: recipe.Description(),
			RunE: func(cmd *cobra.Command, args []string) error {
				cmd.SilenceUsage = true
				if isCustom {
					// Custom recipes need to be reloaded when actually running
					actualRecipe, cleanupRun, err := playground.LoadCustomRecipe(customRecipeName, recipes)
					if err != nil {
						return err
					}
					defer cleanupRun()
					return runIt(actualRecipe)
				}
				return runIt(recipe)
			},
		}
		recipeCmd.Flags().AddFlagSet(recipe.Flags())
		addCommonRecipeFlags(recipeCmd)
		startCmd.AddCommand(recipeCmd)
	}

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(inspectCmd)

	logsCmd.Flags().BoolVarP(&followFlag, "follow", "f", false, "Stream logs continuously instead of displaying and exiting")
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(listCmd)
	portCmd.Flags().BoolVarP(&portListFlag, "list", "l", false, "List all available ports for the service")
	rootCmd.AddCommand(portCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(cleanCmd)
	rootCmd.AddCommand(validateCmd)

	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().StringVar(&outputFlag, "output", "", "Output folder for the artifacts")

	debugCmd.AddCommand(probeCmd)
	debugCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(generateDocsCmd)
	generateCmd.Flags().BoolVar(&generateForce, "force", false, "overwrite existing files")
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(recipesCmd)
	testCmd.Flags().StringVar(&testRPCURL, "rpc", "http://localhost:8545", "Target RPC URL for sending transactions")
	testCmd.Flags().StringVar(&testELRPCURL, "el-rpc", "", "EL RPC URL for chain queries (default: same as --rpc)")
	testCmd.Flags().DurationVar(&testTimeout, "timeout", 2*time.Minute, "Timeout for waiting for transaction receipt")
	rootCmd.AddCommand(testCmd)

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

	out, err := playground.NewOutput(outputFlag)
	if err != nil {
		return err
	}

	slog.Info("Welcome to Builder Playground! ⚡️🤖")
	slog.Info("Session ID: "+greenColor.Sprint(sessionID), "log-level", logLevel)
	slog.Info("Output folder: " + out.Dst())
	slog.Info("")

	if utils.GetSessionTempDirCount() > 20 {
		slog.Warn("Too many temp dirs - please later remove " + utils.TempPlaygroundDirPath() + " (auto-removed at reboot)")
		slog.Info("")
	}

	// parse the overrides
	overrides := map[string]string{}
	for _, val := range withOverrides {
		parts := strings.SplitN(val, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid override format: %s, expected service=val", val)
		}
		overrides[parts[0]] = parts[1]
	}

	slog.Debug("Building artifacts...")
	builder := recipe.Artifacts()
	builder.GenesisDelay(genesisDelayFlag)
	builder.PrefundedAccounts(prefundedAccounts)
	if err := builder.Build(out); err != nil {
		return err
	}

	exCtx := &playground.ExContext{
		Output:   out,
		LogLevel: logLevel,
		// if contender.tps is set, assume contender is enabled
		Contender: &playground.ContenderContext{
			Enabled:     contenderEnabled,
			ExtraArgs:   contenderArgs,
			TargetChain: contenderTarget,
		},
	}

	components := recipe.Apply(exCtx)
	svcManager := playground.NewManifest(sessionID, components)
	svcManager.Bootnode = exCtx.Bootnode

	// generate the dot graph
	slog.Debug("Generating dot graph...")
	dotGraph := svcManager.GenerateDotGraph()
	if err := out.WriteFile("graph.dot", dotGraph); err != nil {
		return err
	}

	if err := svcManager.Validate(out); err != nil {
		return fmt.Errorf("failed to validate manifest: %w", err)
	}

	// save the manifest.json file
	if err := svcManager.SaveJson(out); err != nil {
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

	slog.Info("Waiting for services to get healthy... ⏳")
	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := dockerRunner.WaitForReady(waitCtx); err != nil {
		return fmt.Errorf("failed to wait for service readiness: %w", err)
	}

	// run post hook operations
	if err := svcManager.ExecutePostHookActions(ctx); err != nil {
		return fmt.Errorf("failed to execute post-hook operations: %w", err)
	}

	if err := dockerRunner.RunLifecycleHooks(ctx); err != nil {
		return fmt.Errorf("failed to run lifecycle hooks: %w", err)
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
			if ss.HostPath != "" {
				svcInfo = append(svcInfo, "host_path", ss.HostPath)
			} else {
				svcInfo = append(svcInfo, "image", ss.Image, "tag", ss.Tag)
			}
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

	var exitErr error

	select {
	case <-ctx.Done():
		exitErr = ctx.Err()
		slog.Warn("Stopping...", "error", exitErr)
	case exitErr = <-dockerRunner.ExitErr():
		slog.Warn("Service failed", "error", exitErr)
	case exitErr = <-watchdogErr:
		slog.Warn("Watchdog failed", "error", exitErr)
	case <-timerCh:
		// no exit error
		slog.Info("Timeout reached! Exiting...")
	}

	if err := dockerRunner.Stop(keepFlag); err != nil {
		return fmt.Errorf("failed to stop docker: %w", err)
	}

	return exitErr
}
