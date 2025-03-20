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

// Sign in a natural user to Metaplay Auth using the browser.
type LoginOpts struct {
	UsePositionalArgs

	argAuthProvider string
}

func init() {
	o := LoginOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argAuthProvider, "AUTH_PROVIDER", "Name of the auth provider to use. Defaults to 'metaplay'.")

	cmd := &cobra.Command{
		Use:   "login [AUTH_PROVIDER]",
		Short: "Login to your authentication provider using the browser",
		Long: renderLong(&o, `
			Login to your authentication provider using the browser.

			The default auth provider is 'metaplay'. If you have multiple auth providers configured in your
			'metaplay-project.yaml', you can specify the name of the provider you want to use with the
			argument AUTH_PROVIDER.

			{Arguments}
		`),
		Run: runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *LoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *LoginOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve auth provider.
	authProviderName := coalesceString(o.argAuthProvider, "metaplay")
	authProvider, err := getAuthProvider(project, authProviderName)
	if err != nil {
		return err
	}

	// Project ID to show
	projectID := "n/a"
	if project != nil {
		projectID = project.Config.ProjectHumanID
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Sign In"))
	log.Info().Msg("")
	log.Info().Msgf("Project:       %s", styles.RenderTechnical(projectID))
	log.Info().Msgf("Auth provider: %s", styles.RenderTechnical(authProvider.Name))
	log.Info().Msg("")

	// Login using the active auth provider.
	err = auth.LoginWithBrowser(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	return nil
}
