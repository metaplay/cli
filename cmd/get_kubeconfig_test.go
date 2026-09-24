/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
)

var (
	humanTokens   = &auth.TokenSet{AccessToken: "an-access-token", RefreshToken: "a-refresh-token"}
	machineTokens = &auth.TokenSet{AccessToken: "an-access-token"}
)

func TestWantsDynamicKubeconfig(t *testing.T) {
	tests := []struct {
		name            string
		tokens          *auth.TokenSet
		credentialsType string
		wantDynamic     bool
	}{
		{"human user by default", humanTokens, "", true},
		{"human user asking for dynamic", humanTokens, "dynamic", true},
		{"human user asking for static", humanTokens, "static", false},
		{"machine user by default", machineTokens, "", false},
		{"machine user asking for static", machineTokens, "static", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isDynamic, err := wantsDynamicKubeconfig(test.credentialsType, test.tokens)
			if err != nil {
				t.Fatalf("wantsDynamicKubeconfig: %v", err)
			}
			if isDynamic != test.wantDynamic {
				t.Errorf("isDynamic = %v, want %v", isDynamic, test.wantDynamic)
			}
		})
	}
}

// A dynamic kubeconfig would work for a machine user until its access token
// expired, and then fail on every request with nothing saying why.
func TestWantsDynamicKubeconfig_RefusesAMachineUser(t *testing.T) {
	_, err := wantsDynamicKubeconfig("dynamic", machineTokens)
	if err == nil {
		t.Fatal("a machine user was given a dynamic kubeconfig")
	}
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	if !strings.Contains(cliErr.Suggestion, "--type=static") {
		t.Errorf("suggestion = %q, want it to point at a static kubeconfig", cliErr.Suggestion)
	}
}

func TestWantsDynamicKubeconfig_RefusesAnUnknownType(t *testing.T) {
	if _, err := wantsDynamicKubeconfig("yaml", humanTokens); err == nil {
		t.Error("an unknown credentials type was accepted")
	}
}
