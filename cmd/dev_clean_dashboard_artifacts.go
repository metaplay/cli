/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"path/filepath"

	"github.com/spf13/cobra"
)

type devCleanDashboardArtifactsOpts struct {
	UsePositionalArgs
}

func (o *devCleanDashboardArtifactsOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func init() {
	o := devCleanDashboardArtifactsOpts{}
	var devCleanDashboardArtifactsCmd = &cobra.Command{
		Use:     "clean-dashboard-artifacts",
		Aliases: []string{"clean-dash"},
		Short:   "[debug] Clean cached build artifacts used by the LiveOps Dashboard build process",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Debug command to clean cached build artifacts used by the LiveOps Dashboard build process.
			Removes the 'node_modules/' directories found in the project and SDK, 
			and the 'dist/' directory inside 'Backend/Dashboard/'.

			You can try running this command if you run into otherwise unexplained errors during 
			'metaplay build dashboard' or 'metaplay dev dashboard' that might be caused by out-of-sync dependencies.
		`),
		Example: renderExample(`
			# Clean cached build artifacts used by the LiveOps Dashboard build process
			metaplay dev clean-dashboard-artifacts
		`),
	}
	devCmd.AddCommand(devCleanDashboardArtifactsCmd)
}

func (o *devCleanDashboardArtifactsOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}
	// Resolve project dashboard, project root and sdk root paths.
	dashboardPath := project.GetDashboardDir()
	projectRootPath, err := filepath.Abs(project.RelativeDir)
	if err != nil {
		return err
	}
	sdkPath := project.GetSdkRootDir()

	cleanTemporaryDashboardFiles(projectRootPath, sdkPath, dashboardPath)

	return nil
}
