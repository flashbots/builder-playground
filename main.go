package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/signal"

	"github.com/ferranbt/builder-playground/internal"
	"github.com/spf13/cobra"
)

var outputFlag string
var genesisDelayFlag uint64
var withOverrides []string
var watchdog bool
var dryRun bool

var rootCmd = &cobra.Command{
	Use:   "playground",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var cookCmd = &cobra.Command{
	Use: "cook",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
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
			Short: "",
			Long:  ``,
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
		recipeCmd.Flags().Uint64Var(&genesisDelayFlag, "genesis-delay", internal.MinimumGenesisDelay, "")

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
	svcManager.Validate()

	// generate the dot graph
	dotGraph := svcManager.GenerateDotGraph()
	if err := artifacts.Out.WriteFile("graph.dot", dotGraph); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	dockerRunner := internal.NewDockerRunner(artifacts.Out, svcManager, nil)
	if err := dockerRunner.Run(); err != nil {
		return err
	}

	if watchdog {
		go func() {
			if err := recipe.Watchdog(svcManager); err != nil {
				panic(err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-sig:
		fmt.Println("Stopping...")
	}

	dockerRunner.Stop()
	return nil
}
