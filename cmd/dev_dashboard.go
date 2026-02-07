/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type devDashboardOpts struct {
	UsePositionalArgs

	extraArgs []string
}

func init() {
	o := devDashboardOpts{}

	args := o.Arguments()
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'pnpm dev'.")

	cmd := &cobra.Command{
		Use:     "dashboard",
		Aliases: []string{"dash"},
		Short:   "Run the dashboard Vue.js project locally in development mode",
		Run:     runCommand(&o),
	}

	devCmd.AddCommand(cmd)
}

func (o *devDashboardOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *devDashboardOpts) Run(cmd *cobra.Command) error {
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
	if err := execChildInteractive(dashboardPath, "pnpm", []string{"install"}, nil); err != nil {
		log.Info().Msg("Have you tried running `metaplay dev clean-dashboard-artifacts`? This removes build artifacts before installing dependencies, potentially fixing some problems.")
		return fmt.Errorf("failed to install dashboard dependencies: %s", err)
	}

	// Run the dashboard project in dev mode
	devArgs := append([]string{"dev"}, o.extraArgs...)
	if err := execChildInteractive(dashboardPath, "pnpm", devArgs, nil); err != nil {
		log.Info().Msg("Have you tried running `metaplay dev clean-dashboard-artifacts`? This removes build artifacts before installing dependencies, potentially fixing some problems.")
		return fmt.Errorf("failed to run the LiveOps Dashboard: %s", err)
	}

	// The dashboard terminated normally
	log.Info().Msgf("Dashboard terminated normally")
	return nil
}
