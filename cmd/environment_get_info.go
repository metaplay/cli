package cmd

import (
	"encoding/json"
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// environmentGetInfoCmd represents the environmentGetInfo command
var environmentGetInfoCmd = &cobra.Command{
	Use:   "get-info",
	Short: "Get information about a specific environment",
	Run:   runGetInfoCmd,
}

func init() {
	environmentCmd.AddCommand(environmentGetInfoCmd)
}

func runGetInfoCmd(cmd *cobra.Command, args []string) {
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

	// Fetch the information from the environment via StackAPI.
	envInfo, err := targetEnv.GetDetails()
	if err != nil {
		log.Error().Msgf("Failed to get environment details: %v", err)
		os.Exit(1)
	}

	// Pretty-print as JSON.
	envInfoJson, err := json.MarshalIndent(envInfo, "", "  ")
	if err != nil {
		log.Error().Msgf("Failed to serialize as JSON: %v", err)
		os.Exit(1)
	}
	log.Info().Msg(string(envInfoJson))
}
