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
	personsTokens = &auth.TokenSet{AccessToken: "an-access-token", RefreshToken: "a-refresh-token"}
	machineTokens = &auth.TokenSet{AccessToken: "an-access-token"}
)

// A person gets a dynamic kubeconfig unless they ask otherwise, and a machine
// user a static one: a dynamic kubeconfig refreshes its credential with the
// session's refresh token, which a machine user does not hold.
func TestKubeconfigCredentialsType_DefaultsByWhoIsAsking(t *testing.T) {
	for name, tc := range map[string]struct {
		tokens *auth.TokenSet
		flag   string
		want   string
	}{
		"a person, by default":         {personsTokens, "", "dynamic"},
		"a person asking for static":   {personsTokens, "static", "static"},
		"a machine user, by default":   {machineTokens, "", "static"},
		"a machine user asking static": {machineTokens, "static", "static"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := kubeconfigCredentialsType(tc.flag, tc.tokens)
			if err != nil {
				t.Fatalf("kubeconfigCredentialsType: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Asked for one anyway, a machine user is told why not and what to use: a
// dynamic kubeconfig would work until the access token expired and then fail
// on every request, with nothing saying why.
func TestKubeconfigCredentialsType_RefusesADynamicKubeconfigToAMachineUser(t *testing.T) {
	_, err := kubeconfigCredentialsType("dynamic", machineTokens)
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

func TestKubeconfigCredentialsType_RefusesAnUnknownType(t *testing.T) {
	if _, err := kubeconfigCredentialsType("yaml", personsTokens); err == nil {
		t.Error("an unknown credentials type was accepted")
	}
}
