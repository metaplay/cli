/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import "testing"

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

	// Renaming a provider must not strand its session.
	renamed := selfHostedProvider()
	renamed.Name = "Something Else"
	if selfHosted.Fingerprint() != renamed.Fingerprint() {
		t.Error("renaming a provider changed its fingerprint")
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
