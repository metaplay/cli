package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// environmentGetServerStatusCmd does a post-deploy check on the status of the
// game server, including the following checks:
// - Pods are in the expected state.
// - Client-facing domain name resolves correctly.
// - Game server responds to client traffic.
// - Admin domain name resolves correctly.
// - Admin endpoint responds with a success code.
var environmentGetServerStatusCmd = &cobra.Command{
	Use:   "check-server-status",
	Short: "Check the status of a deployed game server",
	Run:   runCheckServerStatusCmd,
}

func init() {
	environmentCmd.AddCommand(environmentGetServerStatusCmd)
}

func runCheckServerStatusCmd(cmd *cobra.Command, args []string) {
	// Load project config.
	// _, projectConfig, err := resolveProjectConfig()
	// if err != nil {
	// 	log.Error().Msgf("Failed to find project: %v", err)
	// 	os.Exit(1)
	// }

	// Ensure we have fresh tokens.
	tokenSet, err := auth.EnsureValidTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to get credentials: %v", err)
		os.Exit(1)
	}

	// Resolve target environment.
	targetEnv, err := resolveTargetEnvironment(tokenSet)
	if err != nil {
		log.Error().Msgf("Failed to resolve environment: %v", err)
		os.Exit(1)
	}

	// \todo check for server to be deployed?

	// Check the game server status.
	err = targetEnv.WaitForServerToBeReady()
	if err != nil {
		log.Error().Msgf("Server is in failed state: %v", err)
		os.Exit(1)
	}

	log.Info().Msgf("All game server checks successfully completed!")
}
