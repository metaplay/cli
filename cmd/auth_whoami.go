package cmd

import (
	"encoding/json"
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// whoamiCmd displays information about the logged in user
var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show information about the signed in user",
	Run:   runWhoamiCmd,
}

func init() {
	authCmd.AddCommand(whoamiCmd)
}

func runWhoamiCmd(cmd *cobra.Command, args []string) {
	// Ensure we have fresh tokens.
	// \todo handle not-logged-in more gracefully? this now errors out
	tokenSet, err := auth.EnsureValidTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to get credentials: %v", err)
		os.Exit(1)
	}

	// Handle valid tokenSet
	if tokenSet == nil {
		log.Error().Msg("not logged in! sign in first with 'metaplay auth login' or 'metaplay auth machine-login'")
		os.Exit(1)
	}

	// Convert to MetaplayIDToken and print info to console.
	idToken, err := auth.ResolveMetaplayIDToken(tokenSet.IDToken)
	if err != nil {
		log.Panic().Msgf("Failed to resolve ID token: %v", err)
	}
	idTokenJson, err := json.MarshalIndent(idToken, "", "  ")
	if err != nil {
		log.Panic().Msgf("Failed to serialize IDToken to JSON: %v", err)
	}

	// Fetch user info from portal.
	userInfo, err := auth.FetchUserInfo(tokenSet)
	if err != nil {
		log.Panic().Msgf("Failed to fetch user info: %v", err)
	}
	userInfoJSON, err := json.MarshalIndent(userInfo, "", "  ")
	if err != nil {
		log.Panic().Msgf("Failed to marshal user info to JSON: %v", err)
	}

	// Print out the ID token as JSON.
	log.Debug().Msgf("ID token: %s", idTokenJson)

	// Print user info as JSON.
	log.Info().Msgf("User info: %s", userInfoJSON)
}
