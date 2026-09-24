/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zalando/go-keyring"
	clientauthenticationv1beta1 "k8s.io/client-go/pkg/apis/clientauthentication/v1beta1"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
)

// The plugin a dynamic kubeconfig runs. One pointing at the Kubernetes API
// proxy passes --proxy and no StackAPI, which it never asks; any other passes
// the StackAPI it asks for a Kubernetes credential, as every kubeconfig written
// before the proxy does.
func TestGetKubernetesExecCredential_NeedsAStackAPIOnlyWithoutTheProxy(t *testing.T) {
	for name, tc := range map[string]struct {
		opts  getKubernetesExecCredentialOpts
		valid bool
	}{
		"a StackAPI": {getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids", argStackAPIBaseURL: "https://infra.stack.example.com/stackapi"}, true},
		"the proxy":  {getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids", flagProxy: true}, true},
		"neither":    {getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.opts.Prepare(nil, nil)
			if tc.valid && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tc.valid && err == nil {
				t.Error("accepted")
			}
		})
	}
}

// accessTokenExpiringAt is an access token whose exp claim is expiresAt. The
// CLI never checks its signature.
func accessTokenExpiringAt(t *testing.T, subject string, expiresAt time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": subject,
		"exp": expiresAt.Unix(),
	}).SignedString([]byte("not-a-real-key"))
	if err != nil {
		t.Fatalf("failed to sign a token: %v", err)
	}
	return token
}

// signedInProvider is a provider with a person's session stored, whose access
// token expires at expiresAt. Its token endpoint answers a refresh with a token
// expiring at refreshedExpiresAt.
func signedInProvider(t *testing.T, expiresAt, refreshedExpiresAt time.Time) *auth.AuthProviderConfig {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows

	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(auth.TokenSet{
			AccessToken:  accessTokenExpiringAt(t, "refreshed", refreshedExpiresAt),
			RefreshToken: "the-next-refresh-token",
			TokenType:    "bearer",
		})
	}))
	t.Cleanup(endpoint.Close)

	provider := &auth.AuthProviderConfig{Name: "Test Auth", ClientID: "test-client-id", TokenEndpoint: endpoint.URL}
	tokenSet := &auth.TokenSet{
		AccessToken:  accessTokenExpiringAt(t, "stored", expiresAt),
		RefreshToken: "a-refresh-token",
		TokenType:    "bearer",
	}
	if err := auth.SaveSessionState(provider, auth.UserTypeHuman, tokenSet); err != nil {
		t.Fatalf("failed to store a session: %v", err)
	}
	return provider
}

func decodeExecCredential(t *testing.T, payload string) *clientauthenticationv1beta1.ExecCredentialStatus {
	t.Helper()
	var credential clientauthenticationv1beta1.ExecCredential
	if err := json.Unmarshal([]byte(payload), &credential); err != nil || credential.Status == nil {
		t.Fatalf("not an exec credential: %v\n%s", err, payload)
	}
	return credential.Status
}

// A token that would reach its reported expiry within the skew is refreshed
// before it is handed to kubectl, and kubectl is told the refreshed token's
// expiry, a skew early.
func TestProxyExecCredential_RefreshesATokenWithinTheSkewOfItsExpiry(t *testing.T) {
	refreshedExpiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	provider := signedInProvider(t, time.Now().Add(30*time.Second), refreshedExpiresAt)

	payload, err := proxyExecCredential(provider)
	if err != nil {
		t.Fatalf("proxyExecCredential: %v", err)
	}

	status := decodeExecCredential(t, payload)
	if status.Token != accessTokenExpiringAt(t, "refreshed", refreshedExpiresAt) {
		t.Errorf("kubectl was handed the stored token, which expires within the skew")
	}
	if want := refreshedExpiresAt.Add(-envapi.ProxyExecCredentialSkew); status.ExpirationTimestamp == nil || !status.ExpirationTimestamp.Time.Equal(want) {
		t.Errorf("expirationTimestamp = %v, want %v", status.ExpirationTimestamp, want)
	}
}

// One with longer left is handed on as it is.
func TestProxyExecCredential_HandsOnATokenWithLongerLeft(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	provider := signedInProvider(t, expiresAt, time.Now().Add(time.Hour))

	payload, err := proxyExecCredential(provider)
	if err != nil {
		t.Fatalf("proxyExecCredential: %v", err)
	}

	status := decodeExecCredential(t, payload)
	if status.Token != accessTokenExpiringAt(t, "stored", expiresAt) {
		t.Errorf("kubectl was handed another token than the stored one")
	}
}

// With no session, kubectl is told how to get one: it runs the plugin with no
// terminal, so the plugin cannot log in itself.
func TestProxyExecCredential_SaysHowToLogInWithoutASession(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	_, err := proxyExecCredential(&auth.AuthProviderConfig{Name: "Test Auth", ClientID: "test-client-id"})
	if err == nil {
		t.Fatal("answered with no session")
	}
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	if !strings.Contains(cliErr.Suggestion, "metaplay auth login") {
		t.Errorf("suggestion = %q, want it to say how to log in", cliErr.Suggestion)
	}
}
