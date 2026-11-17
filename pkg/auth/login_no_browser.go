/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
)

// LoginWithoutBrowser signs in a human user whose browser cannot reach the CLI's
// loopback callback: over SSH, in a container, or with an agent running the CLI.
// The authorization request returns to the provider's NoBrowserRedirectURI, a page
// that shows a code for the user to paste back.
//
// The login is saved as pending before anything is printed, so it can be finished
// by CompleteLoginWithCode in a later invocation. When codeInput is given, the
// code is also read from it and the login finished here; when it is nil, the CLI
// prints how to finish and returns.
func LoginWithoutBrowser(authProvider *AuthProviderConfig, codeInput io.Reader) error {
	if authProvider.NoBrowserRedirectURI == "" {
		return clierrors.Newf("Auth provider '%s' has no page to show a sign-in code", authProvider.Name).
			WithDetails("Signing in without a browser needs the provider's 'noBrowserRedirectUri'").
			WithSuggestion(fmt.Sprintf("Run '%s' on a machine with a browser", authProvider.LoginCommand()))
	}

	codeVerifier, codeChallenge := generateCodeVerifierAndChallenge()
	state, err := generateRandomString(16)
	if err != nil {
		return fmt.Errorf("failed to generate random state: %w", err)
	}

	err = savePendingLogin(authProvider, pendingLogin{
		State:        state,
		CodeVerifier: codeVerifier,
		RedirectURI:  authProvider.NoBrowserRedirectURI,
	}, time.Now())
	if err != nil {
		return clierrors.Wrap(err, "Failed to save the sign-in")
	}

	authURL := buildAuthorizeURL(authProvider, authProvider.NoBrowserRedirectURI, codeChallenge, state)
	completeCommand := authProvider.LoginCommand() + " --code CODE"

	log.Info().Msg("Open this URL in a browser on any machine, and sign in:")
	log.Info().Msg("")
	log.Info().Msg("  " + authURL)
	log.Info().Msg("")

	if codeInput == nil {
		log.Info().Msgf("The page then shows a code. Finish signing in with %s within %d minutes.",
			styles.RenderPrompt(completeCommand), int(pendingLoginLifetime.Minutes()))
		return nil
	}

	log.Info().Msg("The page then shows a code. Paste it here, or finish later with " + styles.RenderPrompt(completeCommand) + ".")
	fmt.Print("Code: ")
	pasted, err := bufio.NewReader(codeInput).ReadString('\n')
	// A last line without a newline still counts, as when stdin ends after the paste.
	if err != nil && (!errors.Is(err, io.EOF) || pasted == "") {
		return clierrors.Wrap(err, "Failed to read the code").
			WithSuggestion(fmt.Sprintf("Finish signing in with '%s'", completeCommand))
	}

	return CompleteLoginWithCode(authProvider, pasted)
}

// CompleteLoginWithCode finishes a login started by LoginWithoutBrowser, with the
// code the sign-in page showed.
func CompleteLoginWithCode(authProvider *AuthProviderConfig, pasted string) error {
	state, code, err := parsePastedCode(pasted)
	if err != nil {
		return err
	}

	login, err := takePendingLogin(authProvider, state, time.Now())
	if err != nil {
		return err
	}

	tokenSet, err := exchangeCodeForTokens(code, login.CodeVerifier, login.RedirectURI, authProvider)
	if err != nil {
		// The state matched but the code may still have been mistyped, so let it be
		// pasted again. Should the code itself have been spent, pasting it again
		// fails the same way: the provider refuses a code it has already exchanged.
		if restoreErr := restorePendingLogin(authProvider, login, time.Now()); restoreErr != nil {
			log.Debug().Msgf("Failed to restore the pending login: %v", restoreErr)
		}
		return clierrors.Wrap(err, "Failed to exchange the code for tokens").
			WithSuggestion(fmt.Sprintf("Check the code was pasted whole, or run '%s --no-browser' again", authProvider.LoginCommand()))
	}

	if err := SaveSessionState(authProvider, UserTypeHuman, tokenSet); err != nil {
		return clierrors.Wrap(err, "Failed to save tokens")
	}

	return reportSignIn(authProvider, tokenSet)
}

// parsePastedCode splits what the user pasted into the OAuth2 state and the
// authorization code. The sign-in page shows them as '<state>.<code>'; the state
// is hex, so the first dot separates them even though codes contain dots. The
// whole URL the browser was redirected to is accepted too, which is what a user
// has when the page itself does not load.
func parsePastedCode(pasted string) (state, code string, err error) {
	pasted = strings.Trim(strings.TrimSpace(pasted), `"'`)
	if pasted == "" {
		return "", "", clierrors.New("No code was given").
			WithSuggestion("Paste the code the sign-in page showed")
	}

	if strings.HasPrefix(pasted, "https://") || strings.HasPrefix(pasted, "http://") {
		parsed, err := url.Parse(pasted)
		if err != nil {
			return "", "", clierrors.Wrap(err, "The pasted URL is not valid")
		}
		query := parsed.Query()
		if oauthErr := query.Get("error"); oauthErr != "" {
			cliErr := clierrors.Newf("Sign-in failed: %s", oauthErr)
			if description := query.Get("error_description"); description != "" {
				cliErr = cliErr.WithDetails(description)
			}
			return "", "", cliErr
		}
		state, code = query.Get("state"), query.Get("code")
		if state == "" || code == "" {
			return "", "", clierrors.New("The pasted URL carries no sign-in code").
				WithSuggestion("Paste the code the sign-in page showed")
		}
		return state, code, nil
	}

	state, code, found := strings.Cut(pasted, ".")
	if !found || state == "" || code == "" || strings.ContainsAny(pasted, " \t") {
		return "", "", clierrors.New("The pasted text is not a sign-in code").
			WithSuggestion("Copy the whole code from the sign-in page, and paste it as it is")
	}
	return state, code, nil
}
