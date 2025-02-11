/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type BuildDashboardOpts struct{}

func init() {
	o := BuildDashboardOpts{}

	var buildDashboardCmd = &cobra.Command{
		Use:     "dashboard [flags]",
		Aliases: []string{"dash"},
		Short:   "Build the Vue.js LiveOps Dashboard",
		Run:     runCommand(&o),
	}

	buildCmd.AddCommand(buildDashboardCmd)
}

func (o *BuildDashboardOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("no arguments are expected, got %d", len(args))
	}

	return nil
}

func (o *BuildDashboardOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that project uses a custom dashboard, otherwise error out
	if !project.UsesCustomDashboard() {
		return fmt.Errorf("project does not have a custom dashboard to build")
	}

	// Check that required dashboard tools are installed and satisfy version requirements.
	if err := checkDashboardToolVersions(project); err != nil {
		return err
	}

	// Resolve project dashboard path.
	dashboardPath := project.GetDashboardDir()

	// Install dashboard dependencies
	if err := execChildInteractive(dashboardPath, "pnpm", []string{"install"}); err != nil {
		log.Error().Msgf("Failed to install LiveOps Dashboard dependencies: %s", err)
		os.Exit(1)
	}

	// Build the dashboard
	if err := execChildInteractive(dashboardPath, "pnpm", []string{"build"}); err != nil {
		log.Error().Msgf("Failed to build the LiveOps Dashboard: %s", err)
		os.Exit(1)
	}

	// Built done
	log.Info().Msgf("Dashboard built successfully")
	return nil
}
