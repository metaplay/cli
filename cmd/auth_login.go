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

// Sign in a natural user to Metaplay Auth using the browser.
type LoginOpts struct {
}

func init() {
	o := LoginOpts{}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to your Metaplay account using the browser",
		Run:   runCommand(&o),
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
	authProvider := getAuthProvider(project)

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
