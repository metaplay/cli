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

// Run the game server locally.
type devServerOpts struct {
	UsePositionalArgs

	extraArgs []string
}

func init() {
	o := devServerOpts{}

	args := o.Arguments()
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'dotnet run'.")

	cmd := &cobra.Command{
		Use:   "server [flags] [-- EXTRA_ARGS]",
		Short: "Run the .NET game server locally",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run the C# game server locally.

			Also check that the .NET SDK is installed and is a recent enough version.

			This command is roughly equivalent to running:
			Backend/Server$ dotnet run EXTRA_ARGS

			{Arguments}
		`),
		Example: trimIndent(`
			# Run the server. Stop the server by pressing 'q'.
			metaplay dev server

			# Run with specific log level.
			metaplay dev server -- -LogLevel=Warning

			# Pass additional arguments to the game server (dotnet run).
			metaplay dev server -- -ExitAfter=00:00:30
		`),
	}

	devCmd.AddCommand(cmd)
}

func (o *devServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	o.extraArgs = args

	return nil
}

func (o *devServerOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Game Server Locally"))
	log.Info().Msg("")

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Resolve server path.
	serverPath := project.GetServerDir()

	// Build the game server .NET project.
	if err := execChildInteractive(serverPath, "dotnet", []string{"build"}); err != nil {
		return fmt.Errorf("failed to build the game server .NET project: %s", err)
	}

	// Run the game server (skip build).
	runArgs := append([]string{"run", "--no-build"}, o.extraArgs...)
	if err := execChildInteractive(serverPath, "dotnet", runArgs); err != nil {
		return fmt.Errorf("game server exited with error: %s", err)
	}

	// The server exited normally
	log.Info().Msgf("Game server terminated normally")
	return nil
}
