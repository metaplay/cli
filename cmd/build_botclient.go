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
		Run:   runCommand(&o),
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
