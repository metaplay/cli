/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"context"
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
)

// RequireLoggedIn ensures that the user is logged in. If the user is not logged
// in, it will prompt the user to log in.
func RequireLoggedIn(ctx context.Context, authProvider *auth.AuthProviderConfig) (*auth.TokenSet, error) {
	// Check if we're logged in.
	tokenSet, err := auth.LoadAndRefreshTokenSet(authProvider)
	if err != nil {
		return nil, err
	}

	// If already logged in, just return the token set.
	if tokenSet != nil {
		return tokenSet, nil
	}

	// If not yet logged in, ask if we should do it.

	// If not in interactive shell, bail out immediately. This is where a coding
	// agent ends up, so say how a person signs in as well as a machine.
	if !isInteractiveMode {
		suggestion := fmt.Sprintf("Run '%s' if a browser can open on this machine", authProvider.LoginCommand())
		if authProvider.NoBrowserRedirectURI != "" {
			suggestion += fmt.Sprintf(", or '%s --no-browser' if not", authProvider.LoginCommand())
		}
		return nil, clierrors.New("Login required").
			WithSuggestion(suggestion + ". For CI and other unattended use, run 'metaplay auth machine-login'")
	}

	// Confirm the login operation with the user.
	// \todo show auth provider info
	choice, err := DoConfirmDialog(
		ctx,
		"Login Required",
		"Operation requires logging in to Metaplay cloud with your default browser.",
		"Continue?",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to show login confirmation dialog: %w", err)
	}

	// Handle the user's decision.
	if !choice {
		// User declined to log in.
		return nil, fmt.Errorf("user cancelled the operation")
	}

	// User wants to log in.
	err = auth.LoginWithBrowser(ctx, authProvider, true)
	if err != nil {
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	// Load the newly established token set.
	return auth.LoadAndRefreshTokenSet(authProvider)
}
