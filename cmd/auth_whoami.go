/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Display information about the logged in user.
type WhoamiOpts struct {
	flagFormat string
}

func init() {
	o := WhoamiOpts{}

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show information about the signed in user",
		Long: trimIndent(`
			Show information about the signed in user.

			By default, displays the information in a human-readable text format.
			Use --format=json to get the complete user information in JSON format.
		`),
		Example: trimIndent(`
			# Show user information in text format
			metaplay auth whoami

			# Show complete user information in JSON format
			metaplay auth whoami --format=json
		`),
		Run: runCommand(&o),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'")

	authCmd.AddCommand(cmd)
}

func (o *WhoamiOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	return nil
}

func (o *WhoamiOpts) Run(cmd *cobra.Command) error {
	// Load session state.
	sessionState, err := auth.LoadSessionState()
	if err != nil {
		return err
	}

	// Load (and refresh) tokens, if any.
	// \todo get from sessionState directly
	tokenSet, err := auth.LoadAndRefreshTokenSet()
	if err != nil {
		return err
	}

	// Handle valid tokenSet
	if tokenSet == nil {
		log.Info().Msg("Not logged in! You can sign in with 'metaplay auth login' or 'metaplay auth machine-login'")
		return nil
	}

	// Display IDToken if it exists (only for human users).
	if tokenSet.IDToken != "" {
		// Convert to MetaplayIDToken and print info to console.
		idToken, err := auth.ResolveMetaplayIDToken(cmd.Context(), tokenSet.IDToken)
		if err != nil {
			log.Panic().Msgf("Failed to resolve ID token: %v", err)
		}
		idTokenJson, err := json.MarshalIndent(idToken, "", "  ")
		if err != nil {
			log.Panic().Msgf("Failed to serialize IDToken to JSON: %v", err)
		}

		// Print out the ID token as JSON.
		log.Debug().Msgf("ID token: %s", idTokenJson)
	}

	// Fetch user info from portal.
	userInfo, err := auth.FetchUserInfo(tokenSet)
	if err != nil {
		log.Panic().Msgf("Failed to fetch user info: %v", err)
	}

	// Output based on format
	if o.flagFormat == "json" {
		// Pretty-print as JSON
		userInfoJSON, err := json.MarshalIndent(userInfo, "", "  ")
		if err != nil {
			log.Panic().Msgf("Failed to marshal user info to JSON: %v", err)
		}
		log.Info().Msg(string(userInfoJSON))
	} else {
		// Print user info in text format
		log.Info().Msg("")
		log.Info().Msgf("Name:        %s", styles.RenderTechnical(userInfo.Name))
		log.Info().Msgf("Email:       %s", styles.RenderTechnical(userInfo.Email))
		log.Info().Msgf("User type:   %s", styles.RenderTechnical(string(sessionState.UserType)))
		log.Info().Msgf("Picture:     %s", styles.RenderTechnical(coalesceString(userInfo.Picture, "n/a")))
		log.Info().Msgf("Provider ID: %s", styles.RenderTechnical(userInfo.Subject))
		// Note: not showing legacy roles
	}

	return nil
}
