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

type BuildBotClientOpts struct {
}

func init() {
	o := BuildBotClientOpts{}

	cmd := &cobra.Command{
		Use:   "botclient [flags]",
		Short: "Build the BotClient .NET project",
		Long: trimIndent(`
			Build the BotClient .NET project using the .NET SDK.

			This command:
			- Verifies the required .NET SDK version is installed
			- Builds the BotClient project using 'dotnet build'

			The BotClient is used for automated testing and load testing of the game server.
			It simulates real player behavior by running multiple bot instances that connect
			to the server and perform various actions.

			Related commands:
			- 'metaplay deploy botclients ...' to deploy bot clients to a cloud environment
			- 'metaplay dev botclient ...' to run the bot client locally
			- 'metaplay remove botclients ...' to remove bot clients from an environment
			- 'metaplay debug logs ...' to view bot client logs in an environment
		`),
		Example: trimIndent(`
			# Build the BotClient project
			metaplay build botclient

			# Build and then run the bot client locally
			metaplay build botclient && metaplay dev botclient
		`),
		Run: runCommand(&o),
	}

	buildCmd.AddCommand(cmd)
}

func (o *BuildBotClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("no arguments are expected, got %d", len(args))
	}

	return nil
}

func (o *BuildBotClientOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.versionMetadata.MinDotnetSdkVersion); err != nil {
		log.Error().Msgf("Failed to resolve .NET version: %s", err)
		os.Exit(1)
	}

	// Resolve backend root path.
	botClientPath := project.getBotClientDir()

	// Build the project
	if err := execChildTask(botClientPath, "dotnet", []string{"build"}); err != nil {
		log.Error().Msgf("Failed to build the game server .NET project: %s", err)
		os.Exit(1)
	}

	// Server built successfully
	log.Info().Msgf("BotClient .NET project built successfully")
	return nil
}
