/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Display information about the logged in user.
type WhoamiOpts struct {
}

func init() {
	o := WhoamiOpts{}

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show information about the signed in user",
		Run:   runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *WhoamiOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	return nil
}

func (o *WhoamiOpts) Run(cmd *cobra.Command) error {
	// Load (and refresh) tokens, if any.
	tokenSet, err := auth.LoadAndRefreshTokenSet()
	if err != nil {
		return err
	}

	// Handle valid tokenSet
	if tokenSet == nil {
		log.Info().Msg("Not logged in! You can sign in with 'metaplay auth login' or 'metaplay auth machine-login'")
		return nil
	}

	// Convert to MetaplayIDToken and print info to console.
	idToken, err := auth.ResolveMetaplayIDToken(cmd.Context(), tokenSet.IDToken)
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

	return nil
}
