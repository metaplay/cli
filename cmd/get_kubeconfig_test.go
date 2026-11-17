/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
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
		{"machine user asking for dynamic", machineTokens, "dynamic", true},
		{"machine user asking for static", machineTokens, "static", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if isDynamic := wantsDynamicKubeconfig(test.credentialsType, test.tokens); isDynamic != test.wantDynamic {
				t.Errorf("isDynamic = %v, want %v", isDynamic, test.wantDynamic)
			}
		})
	}
}

// An unknown type is refused before Run resolves the environment, which may
// ask to log in first.
func TestGetKubeConfig_PrepareRefusesAnUnknownType(t *testing.T) {
	for _, credentialsType := range []string{"", "dynamic", "static"} {
		o := getKubeConfigOpts{flagCredentialsType: credentialsType}
		if err := o.Prepare(nil, nil); err != nil {
			t.Errorf("type %q refused: %v", credentialsType, err)
		}
	}

	o := getKubeConfigOpts{flagCredentialsType: "yaml"}
	err := o.Prepare(nil, nil)
	if err == nil {
		t.Fatal("an unknown credentials type was accepted")
	}
	if !clierrors.IsUsageError(err) {
		t.Errorf("error = %v, want a usage error", err)
	}
}
