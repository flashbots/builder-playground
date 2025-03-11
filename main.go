package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"

	"github.com/ferranbt/builder-playground/internal"
	"github.com/spf13/cobra"
)

var outputFlag string
var genesisDelayFlag uint64
var withOverrides []string
var watchdog bool
var dryRun bool
var interactive bool

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

var recipes = []internal.Recipe{
	&internal.L1Recipe{},
	&internal.OpRecipe{},
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
		recipeCmd.Flags().Uint64Var(&genesisDelayFlag, "genesis-delay", internal.MinimumGenesisDelay, "")
		recipeCmd.Flags().BoolVar(&interactive, "interactive", false, "interactive mode")

		cookCmd.AddCommand(recipeCmd)
	}

	rootCmd.AddCommand(cookCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runIt(recipe internal.Recipe) error {
	builder := recipe.Artifacts()
	builder.OutputDir(outputFlag)
	builder.GenesisDelay(genesisDelayFlag)
	artifacts, err := builder.Build()
	if err != nil {
		return err
	}

	svcManager := recipe.Apply(artifacts)
	if err := svcManager.Validate(); err != nil {
		return fmt.Errorf("failed to validate manifest: %w", err)
	}

	// generate the dot graph
	dotGraph := svcManager.GenerateDotGraph()
	if err := artifacts.Out.WriteFile("graph.dot", dotGraph); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	dockerRunner, err := internal.NewDockerRunner(artifacts.Out, svcManager, nil, interactive)
	if err != nil {
		return fmt.Errorf("failed to create docker runner: %w", err)
	}

	if err := dockerRunner.Run(); err != nil {
		return fmt.Errorf("failed to run docker: %w", err)
	}

	if !interactive {
		// print services info
		fmt.Printf("Services started:\n==================\n")
		for _, ss := range svcManager.Services() {
			ports := ss.Ports()
			sort.Slice(ports, func(i, j int) bool {
				return ports[i].Name < ports[j].Name
			})

			portsStr := []string{}
			for _, p := range ports {
				portsStr = append(portsStr, fmt.Sprintf("%s: %d/%d", p.Name, p.Port, p.HostPort))
			}
			fmt.Printf("- %s (%s)\n", ss.Name, strings.Join(portsStr, ", "))
		}
	}

	watchdogErr := make(chan error, 1)
	if watchdog {
		go func() {
			if err := recipe.Watchdog(svcManager, artifacts.Out); err != nil {
				watchdogErr <- fmt.Errorf("watchdog failed: %w", err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-sig:
		fmt.Println("Stopping...")
	case err := <-dockerRunner.ExitErr():
		fmt.Println("Service failed:", err)
	case err := <-watchdogErr:
		return err
	}

	if err := dockerRunner.Stop(); err != nil {
		return fmt.Errorf("failed to stop docker: %w", err)
	}
	return nil
}
