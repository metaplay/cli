/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	clierrors "github.com/metaplay/cli/internal/errors"
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
		return clierrors.New("Project does not have a custom dashboard to run")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run LiveOps Dashboard Locally"))
	log.Info().Msg("")

	// Check that required dashboard tools are installed and satisfy version requirements.
	ctx := cmd.Context()
	if err := checkDashboardToolVersions(ctx, project); err != nil {
		return err
	}

	// Resolve project dashboard directory.
	dashboardPath := project.GetDashboardDir()

	// Install dashboard dependencies
	if err := execChildInteractive(ctx, dashboardPath, "pnpm", []string{"install"}, nil); err != nil {
		return clierrors.Wrap(err, "Failed to install dashboard dependencies").
			WithSuggestion("Try `metaplay dev clean-dashboard-artifacts` to remove stale build artifacts before reinstalling.")
	}

	// Run the dashboard project in dev mode
	devArgs := append([]string{"dev"}, o.extraArgs...)
	if err := execChildInteractive(ctx, dashboardPath, "pnpm", devArgs, nil); err != nil {
		return clierrors.Wrap(err, "Failed to run the LiveOps Dashboard").
			WithSuggestion("Try `metaplay dev clean-dashboard-artifacts` to remove stale build artifacts before reinstalling.")
	}

	// The dashboard terminated normally
	log.Info().Msgf("Dashboard terminated normally")
	return nil
}
