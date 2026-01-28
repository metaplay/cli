/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"path/filepath"

	"github.com/spf13/cobra"
)

type devCleanDashboardCachedFilesOpts struct {
	UsePositionalArgs
}

func (o *devCleanDashboardCachedFilesOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func init() {
	o := devCleanDashboardCachedFilesOpts{}
	var devCleanDashboardCachedFilesCmd = &cobra.Command{
		Use:     "clean-dashboard-cached-files",
		Aliases: []string{"clean-dash"},
		Short:   "[debug] Clean cached files used by the LiveOps Dashboard build process",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Debug command to clean cached files used by the LiveOps Dashboard build process.
			Removes the 'node_modules/' directories found in the project and SDK, 
			and the 'dist/' directory inside 'Backend/Dashboard/'.
		`),
		Example: renderExample(`
			# Clean cached files used by the LiveOps Dashboard build process
			metaplay dev clean-dashboard-cached-files
		`),
	}
	devCmd.AddCommand(devCleanDashboardCachedFilesCmd)
}

func (o *devCleanDashboardCachedFilesOpts) Run(cmd *cobra.Command) error {
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
