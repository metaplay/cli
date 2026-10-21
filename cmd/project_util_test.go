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

// The empty name and 'metaplay' both follow METAPLAYCLI_AUTH_PROVIDER_FILE: the file
// replaces Metaplay Auth, so every call site naming 'metaplay' reaches it unchanged.
func TestGetAuthProvider_DefaultNamesFollowProviderFile(t *testing.T) {
	setProviderFile(t)

	for _, name := range []string{"", "metaplay"} {
		provider, err := getAuthProvider(nil, name)
		if err != nil {
			t.Fatalf("getAuthProvider(%q) returned an error: %v", name, err)
		}
		if provider.Name != "Example Platform" {
			t.Errorf("getAuthProvider(%q) = %q, want the provider file's platform", name, provider.Name)
		}
	}
}

// Without the environment variable both spellings mean the built-in provider.
func TestGetAuthProvider_DefaultsToBuiltinWithoutProviderFile(t *testing.T) {
	t.Setenv(auth.AuthProviderFileEnvVar, "")

	for _, name := range []string{"", "metaplay"} {
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

// A provider file's name is free text, so it must not be able to claim the session of
// a provider defined in metaplay-project.yaml. The two sessions stay separate.
func TestGetAuthProvider_FileProviderSessionIsNamespaced(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "auth-provider.yaml")
	if err := os.WriteFile(filePath, []byte("name: Corp SSO\n"+
		"clientId: file-client-id\n"+
		"authEndpoint: https://auth.selfhosted.example.com/oauth2/auth\n"+
		"tokenEndpoint: https://auth.selfhosted.example.com/oauth2/token\n"+
		"revokeEndpoint: https://auth.selfhosted.example.com/oauth2/revoke\n"+
		"userInfoEndpoint: https://portal.selfhosted.example.com/api/external/userinfo\n"), 0600); err != nil {
		t.Fatalf("failed to write provider file: %v", err)
	}
	t.Setenv(auth.AuthProviderFileEnvVar, filePath)

	project := &metaproj.MetaplayProject{
		Config: metaproj.ProjectConfig{
			AuthProviders: map[string]*auth.AuthProviderConfig{
				"corp": {Name: "Corp SSO", ClientID: "project-client-id", TokenEndpoint: "https://sso.corp.example.com/oauth2/token"},
			},
		},
	}

	fromFile, err := getAuthProvider(project, "")
	if err != nil {
		t.Fatalf("getAuthProvider returned an error: %v", err)
	}
	fromProject, err := getAuthProvider(project, "corp")
	if err != nil {
		t.Fatalf("getAuthProvider returned an error: %v", err)
	}

	// Both are named "Corp SSO", but they are different platforms.
	if fromFile.GetSessionID() == fromProject.GetSessionID() {
		t.Errorf("both providers use session ID %q, so one would overwrite the other", fromFile.GetSessionID())
	}
	if fromFile.Fingerprint() == fromProject.Fingerprint() {
		t.Error("different platforms produced the same fingerprint")
	}
}

// Two project providers sharing a display name used to resolve at random, because Go
// randomizes map iteration order. Naming the id stays exact; the name is now an error.
func TestGetAuthProvider_AmbiguousDisplayName(t *testing.T) {
	t.Setenv(auth.AuthProviderFileEnvVar, "")

	project := &metaproj.MetaplayProject{
		Config: metaproj.ProjectConfig{
			AuthProviders: map[string]*auth.AuthProviderConfig{
				"corp-eu": {Name: "Corp SSO", TokenEndpoint: "https://eu.corp.example.com/oauth2/token"},
				"corp-us": {Name: "Corp SSO", TokenEndpoint: "https://us.corp.example.com/oauth2/token"},
			},
		},
	}

	// The ambiguous display name is refused, and names both candidates.
	_, err := getAuthProvider(project, "Corp SSO")
	if err == nil {
		t.Fatal("expected an error for an ambiguous provider name")
	}
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error %v is not a CLIError", err)
	}
	details := strings.Join(cliErr.Details, " ")
	if !strings.Contains(details, "corp-eu") || !strings.Contains(details, "corp-us") {
		t.Errorf("details %q do not name both candidates", details)
	}

	// Naming the id is still exact, and stable across runs.
	for range 20 {
		provider, err := getAuthProvider(project, "corp-eu")
		if err != nil {
			t.Fatalf("getAuthProvider returned an error: %v", err)
		}
		if provider.TokenEndpoint != "https://eu.corp.example.com/oauth2/token" {
			t.Fatalf("TokenEndpoint = %q, want the EU provider", provider.TokenEndpoint)
		}
	}
}
