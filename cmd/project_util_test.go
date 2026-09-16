/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metaproj"
)

const testProviderYAML = `
name: Example Platform
clientId: 11111111-2222-3333-4444-555555555555
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`

// Point METAPLAYCLI_AUTH_PROVIDER_FILE at a valid provider file for one test.
func setProviderFile(t *testing.T) {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "auth-provider.yaml")
	if err := os.WriteFile(filePath, []byte(testProviderYAML), 0600); err != nil {
		t.Fatalf("failed to write provider file: %v", err)
	}
	t.Setenv(auth.AuthProviderFileEnvVar, filePath)
}

// The empty name follows METAPLAYCLI_AUTH_PROVIDER_FILE, so a self-hosted platform
// is reached without naming it on every command.
func TestGetAuthProvider_EmptyNameFollowsProviderFile(t *testing.T) {
	setProviderFile(t)

	provider, err := getAuthProvider(nil, "")
	if err != nil {
		t.Fatalf("getAuthProvider returned an error: %v", err)
	}
	if provider.Name != "Example Platform" {
		t.Errorf("Name = %q, want the provider file's platform", provider.Name)
	}
}

// Naming 'metaplay' must reach the built-in provider even while the environment
// variable points the default elsewhere: otherwise the Metaplay Auth session could
// not be inspected or logged out of, and its refresh token would be stranded.
func TestGetAuthProvider_BuiltinNameIgnoresProviderFile(t *testing.T) {
	setProviderFile(t)

	provider, err := getAuthProvider(nil, builtinAuthProviderName)
	if err != nil {
		t.Fatalf("getAuthProvider returned an error: %v", err)
	}
	if !provider.IsBuiltinMetaplayAuth() {
		t.Errorf("Name = %q, want the built-in Metaplay Auth provider", provider.Name)
	}
	if provider.AuthEndpoint != "https://auth.metaplay.dev/oauth2/auth" {
		t.Errorf("AuthEndpoint = %q, want Metaplay's", provider.AuthEndpoint)
	}
}

// Without the environment variable both spellings mean the same thing.
func TestGetAuthProvider_DefaultsToBuiltinWithoutProviderFile(t *testing.T) {
	t.Setenv(auth.AuthProviderFileEnvVar, "")

	for _, name := range []string{"", builtinAuthProviderName} {
		provider, err := getAuthProvider(nil, name)
		if err != nil {
			t.Fatalf("getAuthProvider(%q) returned an error: %v", name, err)
		}
		if !provider.IsBuiltinMetaplayAuth() {
			t.Errorf("getAuthProvider(%q) = %q, want the built-in Metaplay Auth provider", name, provider.Name)
		}
	}
}

// A project's own provider is still found by ID and by name.
func TestGetAuthProvider_ProjectProvider(t *testing.T) {
	setProviderFile(t)

	project := &metaproj.MetaplayProject{
		Config: metaproj.ProjectConfig{
			AuthProviders: map[string]*auth.AuthProviderConfig{
				"corp": {Name: "Corp SSO"},
			},
		},
	}

	for _, name := range []string{"corp", "Corp SSO"} {
		provider, err := getAuthProvider(project, name)
		if err != nil {
			t.Fatalf("getAuthProvider(%q) returned an error: %v", name, err)
		}
		if provider.Name != "Corp SSO" {
			t.Errorf("getAuthProvider(%q) = %q, want the project's provider", name, provider.Name)
		}
	}
}

// Commands run outside a project directory pass a nil project, because
// tryResolveProject() reports "no project found" as (nil, nil). Naming a provider
// there must be an error, not a nil dereference.
func TestGetAuthProvider_NamedProviderWithoutProject(t *testing.T) {
	t.Setenv(auth.AuthProviderFileEnvVar, "")

	provider, err := getAuthProvider(nil, "MyCorpSSO")
	if err == nil {
		t.Fatalf("expected an error, got provider %v", provider)
	}
	if !strings.Contains(err.Error(), "MyCorpSSO") {
		t.Errorf("error %q does not name the requested provider", err.Error())
	}

	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error %v is not a CLIError, so it would not be displayed with details", err)
	}
	if !strings.Contains(strings.Join(cliErr.Details, " "), metaproj.ConfigFileName) {
		t.Errorf("details %v do not point at %s", cliErr.Details, metaproj.ConfigFileName)
	}
	if cliErr.Suggestion == "" {
		t.Error("expected a suggestion telling the user how to proceed")
	}
}
