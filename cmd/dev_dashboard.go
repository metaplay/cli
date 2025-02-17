/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type RunDashboardOpts struct {
}

func init() {
	o := RunDashboardOpts{}

	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Run the dashboard Vue.js project locally in development mode",
		Run:   runCommand(&o),
	}

	devCmd.AddCommand(cmd)
}

func (o *RunDashboardOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *RunDashboardOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that project uses a custom dashboard, otherwise error out
	if !project.UsesCustomDashboard() {
		return fmt.Errorf("project does not have a custom dashboard to run")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run LiveOps Dashboard Locally"))
	log.Info().Msg("")

	// Check that required dashboard tools are installed and satisfy version requirements.
	if err := checkDashboardToolVersions(project); err != nil {
		return err
	}

	// Resolve project dashboard directory.
	dashboardPath := project.GetDashboardDir()

	// Install dashboard dependencies
	if err := execChildInteractive(dashboardPath, "pnpm", []string{"install"}); err != nil {
		return fmt.Errorf("failed to build the LiveOps Dashboard: %s", err)
	}

	// Run the dashboard project in dev mode
	if err := execChildInteractive(dashboardPath, "pnpm", []string{"dev"}); err != nil {
		return fmt.Errorf("failed to run the LiveOps Dashboard: %s", err)
	}

	// The dashboard terminated normally
	log.Info().Msgf("Dashboard terminated normally")
	return nil
}
