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
		Long: renderLong(&o, `
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
	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	return nil
}

func (o *WhoamiOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	authProvider := getAuthProvider(project)

	// Load session state.
	sessionState, err := auth.LoadSessionState(authProvider.GetSessionID())
	if err != nil {
		return err
	}

	// Load (and refresh) tokens, if any.
	// \todo get from sessionState directly
	tokenSet, err := auth.LoadAndRefreshTokenSet(authProvider)
	if err != nil {
		return err
	}

	// Handle valid tokenSet.
	if tokenSet == nil {
		log.Info().Msg("Not logged in! You can sign in with 'metaplay auth login' or 'metaplay auth machine-login'")
		return nil
	}

	// Fetch user info from portal.
	log.Debug().Msgf("Fetch user info...")
	userInfo, err := auth.FetchUserInfo(authProvider, tokenSet)
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
		// Project ID to show
		projectID := "n/a"
		if project != nil {
			projectID = project.Config.ProjectHumanID
		}

		// Print user info in text format
		log.Info().Msg("")
		log.Info().Msgf("Project:       %s", styles.RenderTechnical(projectID))
		log.Info().Msgf("Auth provider: %s", styles.RenderTechnical(authProvider.Name))
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
