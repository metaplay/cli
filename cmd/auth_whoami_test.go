/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"strings"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
)

func TestUserInfoFetchError(t *testing.T) {
	unauthorized := &auth.UserInfoError{StatusCode: 401, Status: "401 Unauthorized", Message: "User not found with Metaplay Auth ID"}
	defaultProvider := &auth.AuthProviderConfig{Name: "metaplay"}
	customProvider := &auth.AuthProviderConfig{Name: "custom"}

	tests := []struct {
		name           string
		err            error
		provider       *auth.AuthProviderConfig
		userType       auth.UserType
		wantSuggestion string // substring; empty means no suggestion
	}{
		{"human on default provider gets portal hint", unauthorized, defaultProvider, auth.UserTypeHuman, "developer portal"},
		{"forbidden counts as refused token", &auth.UserInfoError{StatusCode: 403, Status: "403 Forbidden"}, defaultProvider, auth.UserTypeHuman, "developer portal"},
		{"machine user gets machine-login hint", unauthorized, defaultProvider, auth.UserTypeMachine, "metaplay auth machine-login"},
		{"custom provider gets login hint", unauthorized, customProvider, auth.UserTypeHuman, "Sign in again with 'metaplay auth login'"},
		{"server error gets no hint", &auth.UserInfoError{StatusCode: 503, Status: "503 Service Unavailable"}, defaultProvider, auth.UserTypeHuman, ""},
		{"network error gets no hint", errors.New("failed to fetch userinfo: connection refused"), defaultProvider, auth.UserTypeHuman, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := userInfoFetchError(tt.err, tt.provider, tt.userType)

			cliErr, ok := clierrors.AsCLIError(err)
			if !ok {
				t.Fatalf("error %v is not a *CLIError", err)
			}
			if cliErr.Message != "Failed to fetch user info" {
				t.Errorf("Message = %q", cliErr.Message)
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("error does not wrap the cause %v", tt.err)
			}
			if tt.wantSuggestion == "" && cliErr.Suggestion != "" {
				t.Errorf("Suggestion = %q, want none", cliErr.Suggestion)
			}
			if !strings.Contains(cliErr.Suggestion, tt.wantSuggestion) {
				t.Errorf("Suggestion = %q, want it to contain %q", cliErr.Suggestion, tt.wantSuggestion)
			}
		})
	}
}
