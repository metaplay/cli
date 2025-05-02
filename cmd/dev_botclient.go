/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type devBotClientOpts struct {
	UsePositionalArgs

	extraArgs       []string
	flagEnvironment string
}

func init() {
	o := devBotClientOpts{}

	args := o.Arguments()
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'dotnet run'.")

	cmd := &cobra.Command{
		Use:   "botclient [flags] [-- EXTRA_ARGS]",
		Short: "Run BotClient locally (against local or remote server)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run simulated bots against the locally running server, or a cloud environment.

			{Arguments}

			Related commands:
			- 'metaplay dev server' runs the game server locally.
			- 'metaplay dev dashboard' runs the LiveOps Dashboard locally.
			- 'metaplay build botclient' builds the BotClient project.
		`),
		Example: renderExample(`
			# Run bots against the locally running server.
			metaplay dev botclient

			# Run bots against the 'tough-falcons' cloud environment.
			metaplay dev botclient -e tough-falcons

			# Pass additional arguments to 'dotnet run' of the BotClient project.
			metaplay dev botclient -- -MaxBots=5 -MaxBotId=20
		`),
	}

	devCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagEnvironment, "environment", "e", "", "Environment (from metaplay-project.yaml) to run the bots against.")
}

func (o *devBotClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *devBotClientOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Bot Client Locally"))
	log.Info().Msg("")

	// Resolve target environment flags (if specified)
	targetEnvFlags := []string{}
	if o.flagEnvironment != "" {
		// Resolve project and environment.
		envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.flagEnvironment)
		if err != nil {
			return err
		}

		// Create TargetEnvironment.
		targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

		// Fetch environment info.
		envInfo, err := targetEnv.GetDetails()
		if err != nil {
			return err
		}

		// Construct flags for botclient.
		targetEnvFlags = []string{
			fmt.Sprintf("--Bot:ServerHost=%s", envInfo.Deployment.ServerHostname),
			"--Bot:EnableTls=true",
		}

		log.Debug().Msgf("Flags to run against environment %s: %v", o.flagEnvironment, targetEnvFlags)
	}

	// Check for .NET SDK installation and required version (based on SDK version).
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Resolve botclient path.
	botClientPath := project.GetBotClientDir()

	// Build the BotClient project
	if err := execChildInteractive(botClientPath, "dotnet", []string{"build"}, commonDotnetEnvVars); err != nil {
		log.Error().Msgf("Failed to build the BotClient .NET project: %s", err)
		os.Exit(1)
	}

	// Run the project without rebuilding
	botRunFlags := append([]string{"run", "--no-build"}, targetEnvFlags...)
	botRunFlags = append(botRunFlags, o.extraArgs...)
	if err := execChildInteractive(botClientPath, "dotnet", botRunFlags, commonDotnetEnvVars); err != nil {
		log.Error().Msgf("BotClient exited with error: %s", err)
		os.Exit(1)
	}

	// BotClients terminated normally
	log.Info().Msgf("BotClient terminated normally")
	return nil
}
