/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/styles"
)

// Sign in a natural user to Metaplay Auth using the browser.
type authLoginOpts struct {
	UsePositionalArgs

	argAuthProvider string
	flagNoBrowser   bool
	flagCode        string
}

func init() {
	o := authLoginOpts{}

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

			By default, the login opens a browser on this machine and receives the result on a local
			port. When the browser is on another machine, such as when the CLI runs over SSH, in a
			container or under a coding agent, use --no-browser instead. It prints a URL to open in any
			browser, and the page shown after signing in gives a code to paste back. In a terminal,
			the CLI waits for the code. Otherwise, finish the login with --code within 30 minutes.

			{Arguments}
		`),
		Example: renderExample(`
			# Sign in with a browser on this machine.
			metaplay auth login

			# Sign in with a browser on another machine, pasting the code when asked.
			metaplay auth login --no-browser

			# Finish a --no-browser login started without a terminal, such as by an agent.
			metaplay auth login --code 'CODE'
		`),
		Run: runCommand(&o),
	}

	authCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVar(&o.flagNoBrowser, "no-browser", false, "Sign in with a browser on another machine, by pasting the code it shows")
	flags.StringVar(&o.flagCode, "code", "", "Finish a login started with --no-browser, with the code the sign-in page showed")
	cmd.MarkFlagsMutuallyExclusive("no-browser", "code")
}

func (o *authLoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *authLoginOpts) Run(cmd *cobra.Command) error {
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

	// Finish a login started earlier without a browser. An empty --code, such as
	// from an unset variable, is refused rather than starting a browser login.
	if cmd.Flags().Changed("code") {
		return auth.CompleteLoginWithCode(authProvider, o.flagCode)
	}

	// Start a login whose browser is elsewhere. Wait for the code only when someone
	// can type it: an agent's shell has no terminal, or runs as CI, and finishes
	// with --code.
	if o.flagNoBrowser {
		var codeInput io.Reader
		if tui.IsInteractiveMode() && (isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())) {
			codeInput = os.Stdin
		}
		return auth.LoginWithoutBrowser(authProvider, codeInput)
	}

	// Login using the active auth provider.
	err = auth.LoginWithBrowser(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	return nil
}
