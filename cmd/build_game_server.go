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

type BuildServerOpts struct {
}

func init() {
	o := BuildServerOpts{}

	cmd := &cobra.Command{
		Use:   "game-server [flags]",
		Short: "Build the game server .NET project",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Build the game server .NET project locally.

			Also check that the .NET SDK is installed and is a recent enough version.

			This command is roughly equivalent to:
			Backend/Server$ dotnet build

			Related commands:
			- 'metaplay build dashboard' builds the LiveOps Dashboard.
			- 'metaplay build botclient' builds the BotClient project.
			- 'metaplay build docker-image' builds the docker image with the server and dashboard.
			- 'metaplay run game-server' runs the game server locally.
		`),
		Example: trimIndent(`
			# Build the game server
			metaplay build game-server
		`),
	}

	buildCmd.AddCommand(cmd)
}

func (o *BuildServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("no arguments are expected, got %d", len(args))
	}

	return nil
}

func (o *BuildServerOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.versionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Resolve server path.
	serverPath := project.getServerDir()

	// Build the project.
	if err := execChildTask(serverPath, "dotnet", []string{"build"}); err != nil {
		log.Error().Msgf("Failed to build the game server .NET project: %s", err)
		os.Exit(1)
	}

	// Server built successfully.
	log.Info().Msgf("Server .NET project built successfully")
	return nil
}
