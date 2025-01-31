/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Run the game server locally.
type RunServerOpts struct {
	extraArgs []string
}

func init() {
	o := RunServerOpts{}

	cmd := &cobra.Command{
		Use:   "server [flags] [-- EXTRA_ARGS]",
		Short: "Run the .NET game server locally",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Run the C# game server locally.

			Also check that the .NET SDK is installed and is a recent enough version.

			This command is roughly equivalent to running:
			Backend/Server$ dotnet run EXTRA_ARGS

			Arguments:
			- EXTRA_ARGS is passed directly to 'dotnet run'.
		`),
		Example: trimIndent(`
			# Run the server normally (until terminated).
			metaplay run server

			# Pass additional arguments to the game server (dotnet run).
			metaplay run server -- -ExitAfter=00:00:30
		`),
	}

	runCmd.AddCommand(cmd)
}

func (o *RunServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	o.extraArgs = args

	return nil
}

func (o *RunServerOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.versionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Resolve server path.
	serverPath := project.getServerDir()

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
