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

type buildBotClientOpts struct {
}

func init() {
	o := buildBotClientOpts{}

	cmd := &cobra.Command{
		Use:   "botclient [flags]",
		Short: "Build the BotClient .NET project",
		Long: renderLong(&o, `
			Build the BotClient .NET project using the .NET SDK.

			This command:
			- Verifies the required .NET SDK version is installed
			- Builds the BotClient project using 'dotnet build'

			The BotClient is used for automated testing and load testing of the game server.
			It simulates real player behavior by running multiple bot instances that connect
			to the server and perform various actions.

			Related commands:
			- 'metaplay deploy botclient ...' to deploy bot clients to a cloud environment
			- 'metaplay dev botclient ...' to run the bot client locally
			- 'metaplay remove botclient ...' to remove bot clients from an environment
			- 'metaplay debug logs ...' to view bot client logs in an environment
		`),
		Example: renderExample(`
			# Build the BotClient project
			metaplay build botclient

			# Build and then run the bot client locally
			metaplay build botclient && metaplay dev botclient
		`),
		Run: runCommand(&o),
	}

	buildCmd.AddCommand(cmd)
}

func (o *buildBotClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *buildBotClientOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build Bot Client Locally"))
	log.Info().Msg("")

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		log.Error().Msgf("Failed to resolve .NET version: %s", err)
		os.Exit(1)
	}

	// Resolve backend root path.
	botClientPath := project.GetBotClientDir()

	// Build the project
	if err := execChildTask(botClientPath, "dotnet", []string{"build"}); err != nil {
		log.Error().Msgf("Failed to build the game server .NET project: %s", err)
		os.Exit(1)
	}

	// Server built successfully
	log.Info().Msgf("BotClient .NET project built successfully")
	return nil
}
