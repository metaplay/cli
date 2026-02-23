/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type authLogoutOpts struct {
	UsePositionalArgs

	argAuthProvider string
}

func init() {
	o := authLogoutOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argAuthProvider, "AUTH_PROVIDER", "Name of the auth provider to use. Defaults to 'metaplay'.")

	cmd := &cobra.Command{
		Use:   "logout [AUTH_PROVIDER]",
		Short: "Sign out from the target authentication provider",
		Long: renderLong(&o, `
			Delete the locally persisted credentials to sign out from the target authentication provider.

			The default auth provider is 'metaplay'. If you have multiple auth providers configured in your
			'metaplay-project.yaml', you can specify the name of the provider you want to use with the
			argument AUTH_PROVIDER.

			{Arguments}
		`),
		Run: runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *authLogoutOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *authLogoutOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve auth provider.
	authProvider, err := getAuthProvider(project, o.argAuthProvider)
	if err != nil {
		return err
	}

	// Resolve project ID (if any).
	projectID := "n/a"
	if project != nil {
		projectID = project.Config.ProjectHumanID
	}

	// Show info.
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Sign Out"))
	log.Info().Msg("")
	log.Info().Msgf("Project:       %s", styles.RenderTechnical(projectID))
	log.Info().Msgf("Auth provider: %s", styles.RenderTechnical(authProvider.Name))
	log.Info().Msg("")

	// Check if we're logged in.
	sessionState, err := auth.LoadSessionState(authProvider.GetSessionID())
	if err != nil {
		return err
	}

	// If not logged in, just exit.
	if sessionState == nil {
		log.Info().Msg("ℹ️ You are not logged in to this auth provider, so there's nothing to sign out from.")
		return nil
	}

	// Revoke tokens server-side and delete the local session state.
	err = auth.RevokeAndDeleteSession(authProvider, authProvider.GetSessionID())
	if err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Successfully logged out!"))
	return nil
}
