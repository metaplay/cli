/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	clierrors "github.com/metaplay/cli/internal/errors"
)

func selfHostedProvider() *AuthProviderConfig {
	return &AuthProviderConfig{
		Name:          "Corp SSO",
		ClientID:      "file-client-id",
		TokenEndpoint: "https://auth.selfhosted.example.com/oauth2/token",
	}
}

func projectProvider() *AuthProviderConfig {
	return &AuthProviderConfig{
		Name:          "Corp SSO",
		ClientID:      "project-client-id",
		TokenEndpoint: "https://sso.corp.example.com/oauth2/token",
	}
}

// Sessions are keyed by the provider's name, so two providers can share a key. The
// fingerprint is what keeps one platform's tokens from being presented to the other.
func TestSessionBelongsToProvider(t *testing.T) {
	selfHosted := selfHostedProvider()
	project := projectProvider()

	minted := PersistedSessionState{ProviderPrint: selfHosted.Fingerprint()}

	if !sessionBelongsToProvider(minted, selfHosted) {
		t.Error("a session was rejected by the provider that minted it")
	}
	if sessionBelongsToProvider(minted, project) {
		t.Error("a session minted by another platform was accepted")
	}
}

// Upgrading the CLI must not sign everyone out: sessions stored before the fingerprint
// field existed carry none, and are accepted until the next save stamps them.
func TestSessionBelongsToProvider_AcceptsUnstampedSession(t *testing.T) {
	unstamped := PersistedSessionState{TokenSetGCM: "..."}

	if !sessionBelongsToProvider(unstamped, selfHostedProvider()) {
		t.Error("a session stored before fingerprints existed was rejected")
	}
}

// The fingerprint has to follow the platform and client, not the display name.
func TestFingerprint(t *testing.T) {
	selfHosted := selfHostedProvider()

	if selfHosted.Fingerprint() == projectProvider().Fingerprint() {
		t.Error("two different platforms sharing a name produced the same fingerprint")
	}

	// A different client on the same auth server is a different audience.
	sameServerOtherClient := selfHostedProvider()
	sameServerOtherClient.ClientID = "another-client-id"
	if selfHosted.Fingerprint() == sameServerOtherClient.Fingerprint() {
		t.Error("two different clients produced the same fingerprint")
	}

	// A rename moves the session, because the key is the name. The fingerprint must not
	// move with it, or a rename becomes a mismatch on top of a lost session.
	renamed := selfHostedProvider()
	renamed.Name = "Something Else"
	if selfHosted.Fingerprint() != renamed.Fingerprint() {
		t.Error("renaming a provider changed its fingerprint")
	}
}

// A mismatch has to leave LoadAndRefreshTokenSet intact. displayError prints only the
// outermost suggestion, so wrapping swaps the hint that resolves it for a bare
// 'metaplay auth login' that signs in to the default provider and changes nothing.
func TestLoadAndRefreshTokenSet_ProviderMismatchKeepsItsOwnSuggestion(t *testing.T) {
	configPath := redirectConfigHome(t)

	asking := projectProvider()
	asking.SetProjectKey("corp")
	writePersistedSession(t, configPath, asking.GetSessionID(), PersistedSessionState{
		UserType:      UserTypeHuman,
		TokenSetGCM:   "irrelevant, the fingerprint is checked first",
		ProviderPrint: selfHostedProvider().Fingerprint(),
	})

	_, err := LoadAndRefreshTokenSet(asking)
	if err == nil {
		t.Fatal("a session minted by another provider was accepted")
	}
	// Passes either way: CLIError.Unwrap plus WithCause lets errors.Is see through the
	// generic wrapper too. This pins the cause, not the fix.
	if !errors.Is(err, ErrSessionProviderMismatch) {
		t.Fatalf("error does not report a provider mismatch: %v", err)
	}

	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	// These two pin the fix. displayError shows the outermost message and suggestion and
	// nothing else, so wrapped the user reads "Failed to load stored credentials" over a
	// hint that signs in to the wrong provider.
	if !strings.Contains(cliErr.Message, "belongs to a different auth provider") {
		t.Errorf("message = %q, want the mismatch reported rather than a generic wrapper", cliErr.Message)
	}
	// The suggestion must name the authProviders key: this provider is project-defined,
	// so a bare login would resolve the default one.
	if !strings.Contains(cliErr.Suggestion, "metaplay auth login corp") {
		t.Errorf("suggestion = %q, want it to name the provider's key", cliErr.Suggestion)
	}
}

// A mismatch is the only error LoadSessionState returns that carries its own recovery.
// Everything else keeps the generic wrapper, so an unreadable config is not reported as
// a mismatch.
func TestLoadAndRefreshTokenSet_OtherFailuresStayWrapped(t *testing.T) {
	configPath := redirectConfigHome(t)
	if err := os.WriteFile(configPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := LoadAndRefreshTokenSet(projectProvider())
	if err == nil {
		t.Fatal("an unreadable config was accepted")
	}
	if errors.Is(err, ErrSessionProviderMismatch) {
		t.Errorf("an unparseable config was reported as a provider mismatch: %v", err)
	}

	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	if cliErr.Message != "Failed to load stored credentials" {
		t.Errorf("message = %q, want the generic wrapper to still apply here", cliErr.Message)
	}
	if cliErr.Suggestion == "" {
		t.Error("the generic wrapper lost its suggestion")
	}
}

// LoginCommand names the provider only when a bare login would not reach it.
func TestLoginCommand(t *testing.T) {
	if got := NewMetaplayAuthProvider().LoginCommand(); got != "metaplay auth login" {
		t.Errorf("built-in provider: LoginCommand() = %q, want a bare login", got)
	}

	fromFile, err := parseAuthProviderConfig([]byte(validProviderYAML))
	if err != nil {
		t.Fatalf("parseAuthProviderConfig returned an error: %v", err)
	}
	// A provider file replaces what the default resolves to, so a bare login reaches it.
	if got := fromFile.LoginCommand(); got != "metaplay auth login" {
		t.Errorf("file provider: LoginCommand() = %q, want a bare login", got)
	}

	// Resolution matches the authProviders key first, and the display name is not a
	// reliable argument, so the hint must not reach for it.
	project := projectProvider()
	project.SetProjectKey("corp")
	got := project.LoginCommand()
	if got != "metaplay auth login corp" {
		t.Errorf("project provider: LoginCommand() = %q, want the key named", got)
	}
	if strings.Contains(got, project.Name) {
		t.Errorf("project provider: LoginCommand() = %q, want the key rather than the display name", got)
	}

	// Taking the built-in name earns no project provider the bare command, which
	// resolves the default provider rather than this one.
	impostor := &AuthProviderConfig{Name: metaplayAuthProviderName}
	impostor.SetProjectKey("sneaky")
	if got := impostor.LoginCommand(); got != "metaplay auth login sneaky" {
		t.Errorf("project provider using the built-in name: LoginCommand() = %q, want the key named", got)
	}
}

// redirectConfigHome points the CLI's config at a temp home for the rest of the test and
// returns the path config.json resolves to there. Call it once per test: a second call
// moves the home again and strands what the first wrote. The path comes from the
// production resolver, so the platform layout keeps one definition. Uses t.Setenv, so
// callers cannot be parallel.
func redirectConfigHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows

	path, err := resolvePersistedConfigFilePath()
	if err != nil {
		t.Fatalf("failed to resolve the config path: %v", err)
	}
	return path
}

// writePersistedSession stores one session in the config at the given path.
func writePersistedSession(t *testing.T, configPath, sessionID string, session PersistedSessionState) {
	t.Helper()

	config := newPersistedConfig()
	config.Sessions[sessionID] = session

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

// A provider file's name is free text, so its session is namespaced away from the
// names a project can define.
func TestGetSessionID_FileProviderIsNamespaced(t *testing.T) {
	fromFile, err := parseAuthProviderConfig([]byte(validProviderYAML))
	if err != nil {
		t.Fatalf("parseAuthProviderConfig returned an error: %v", err)
	}

	fromProject := &AuthProviderConfig{Name: fromFile.Name}

	if fromFile.GetSessionID() == fromProject.GetSessionID() {
		t.Errorf("file and project providers share session ID %q", fromFile.GetSessionID())
	}
	if fromFile.GetSessionID() != fileAuthProviderSessionPrefix+fromFile.Name {
		t.Errorf("GetSessionID() = %q, want the file namespace", fromFile.GetSessionID())
	}

	// The built-in provider's key must not move: everyone already has one on disk.
	if NewMetaplayAuthProvider().GetSessionID() != metaplayAuthProviderName {
		t.Error("the built-in provider's session ID changed, which would sign existing users out")
	}
}

// A save stores the session in a config.json only its owner can read, and
// leaves nothing else behind but the lock file.
func TestSaveSessionState_LeavesTheConfigAndItsLock(t *testing.T) {
	keyring.MockInit()
	configPath := redirectConfigHome(t)
	provider := selfHostedProvider()

	// The second save replaces a config that exists.
	for _, accessToken := range []string{"first", "second"} {
		if err := SaveSessionState(provider, UserTypeHuman, &TokenSet{AccessToken: accessToken}); err != nil {
			t.Fatalf("SaveSessionState: %v", err)
		}
	}

	stored, err := LoadSessionState(provider)
	if err != nil || stored == nil {
		t.Fatalf("the session is gone: %v", err)
	}
	if stored.TokenSet.AccessToken != "second" {
		t.Errorf("stored access token = %q, want the second", stored.TokenSet.AccessToken)
	}

	entries, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("failed to list the config directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"config.json", "config.json.lock"}; !slices.Equal(names, want) {
		t.Errorf("config directory holds %v, want %v", names, want)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("failed to stat the config: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("config mode = %v, want 0600", perm)
		}
	}
}
