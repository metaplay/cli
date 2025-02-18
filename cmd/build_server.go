/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type buildServerOpts struct {
}

func init() {
	o := buildServerOpts{}

	cmd := &cobra.Command{
		Use:   "server [flags]",
		Short: "Build the game server .NET project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Build the game server .NET project locally.

			Also check that the .NET SDK is installed and is a recent enough version.

			This command is roughly equivalent to:
			Backend/Server$ dotnet build

			Related commands:
			- 'metaplay build dashboard' builds the LiveOps Dashboard.
			- 'metaplay build botclient' builds the BotClient project.
			- 'metaplay build image' builds a Docker image with the server and dashboard.
			- 'metaplay dev server' runs the game server locally.
		`),
		Example: trimIndent(`
			# Build the game server
			metaplay build game-server
		`),
	}

	buildCmd.AddCommand(cmd)
}

func (o *buildServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *buildServerOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build Game Server Locally"))
	log.Info().Msg("")

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Resolve server path.
	serverPath := project.GetServerDir()

	// Build the project.
	if err := execChildTask(serverPath, "dotnet", []string{"build"}); err != nil {
		log.Error().Msgf("Failed to build the game server .NET project: %s", err)
		os.Exit(1)
	}

	// Server built successfully.
	log.Info().Msgf("Server .NET project built successfully")
	return nil
}
