/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/metaplay/cli/pkg/auth"
)

// RequireLoggedIn ensures that the user is logged in. If the user is not logged
// in, it will prompt the user to log in.
func RequireLoggedIn(ctx context.Context) (*auth.TokenSet, error) {
	// Check if we're logged in.
	tokenSet, err := auth.LoadAndRefreshTokenSet()
	if err != nil {
		return nil, err
	}

	// If already logged in, just return the token set.
	if tokenSet != nil {
		return tokenSet, nil
	}

	// If not yet logged in, ask if we should do it.

	// If not in interactive shell, bail out immediately.
	if !isInteractiveMode {
		return nil, fmt.Errorf("login required, use 'metaplay auth machine-login' to login in non-interactive environments")
	}

	// Confirm the login operation with the user.
	p := tea.NewProgram(newConfirmDialog(
		ctx,
		"Login Required",
		"Operation requires logging in to Metaplay cloud with your default browser.",
		"Continue?",
	))
	m, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to run confirmation dialog: %v", err)
	}

	// Handle the user's decision.
	if m.(confirmDialog).choice {
		// User wants to log in
		err = auth.LoginWithBrowser(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to login: %v", err)
		}
	} else {
		// User declined to log in
		return nil, fmt.Errorf("user cancelled the operation")
	}

	// Load the newly established token set.
	return auth.LoadAndRefreshTokenSet()
}
