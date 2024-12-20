package cmd

import (
	"encoding/json"
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// showTokensCmd represents the showTokens command
var showTokensCmd = &cobra.Command{
	Use:   "show-tokens",
	Short: "Print the active tokens as JSON to stdout",
	Long:  `Print the currently active authentication tokens to stdout.`,
	Run:   runShowTokensCmd,
}

func init() {
	showTokensCmd.Hidden = true
	authCmd.AddCommand(showTokensCmd)
}

func runShowTokensCmd(cmd *cobra.Command, args []string) {
	// Load tokenSet from keyring.
	tokenSet, err := auth.LoadTokenSet()
	if err != nil {
		log.Panic().Msgf("Failed to load tokens: %v", err)
	}

	// Handle valid tokenSet
	if tokenSet != nil {
		// Marshal tokenSet to JSON.
		bytes, err := json.MarshalIndent(tokenSet, "", "  ")
		if err != nil {
			log.Panic().Msgf("failed to serialize tokens into JSON: %v", err)
		}

		// Print as string.
		log.Info().Msg(string(bytes))
	} else {
		log.Info().Msg("Not logged in! Sign in first with 'metaplay auth login' or 'metaplay auth machine-login'")
		os.Exit(1)
	}
}
