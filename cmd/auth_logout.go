/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type LogoutOpts struct {
}

func init() {
	o := LogoutOpts{}

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Sign out from Metaplay cloud",
		Long:  `Delete the locally persisted credentials to sign out from Metaplay cloud.`,
		Run:   runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *LogoutOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *LogoutOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	authProvider := getAuthProvider(project)

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Sign Out"))
	log.Info().Msg("")
	log.Info().Msgf("Project:       %s", styles.RenderTechnical(project.Config.ProjectHumanID))
	log.Info().Msgf("Auth provider: %s", styles.RenderTechnical(authProvider.Name))
	log.Info().Msg("")

	// Check if we're logged in.
	sessionState, err := auth.LoadSessionState(authProvider.GetSessionID())
	if err != nil {
		return err
	}

	// If not logged in, just exit.
	if sessionState == nil {
		log.Info().Msg("")
		log.Info().Msg("Not logged in!")
		return nil
	}

	// Delete the session state.
	err = auth.DeleteSessionState(authProvider.GetSessionID())
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully logged out!"))
	return nil
}
