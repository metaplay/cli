/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	clientauthenticationv1beta1 "k8s.io/client-go/pkg/apis/clientauthentication/v1beta1"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
)

func TestGetKubernetesExecCredential_NeedsAStackAPIOnlyWithoutProxy(t *testing.T) {
	tests := []struct {
		name  string
		opts  getKubernetesExecCredentialOpts
		valid bool
	}{
		{"with STACK_API", getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids", argStackAPIBaseURL: "https://infra.stack.example.com/stackapi"}, true},
		{"with --proxy", getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids", flagProxy: true}, true},
		{"with neither", getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.opts.Prepare(nil, nil)
			if test.valid && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !test.valid && err == nil {
				t.Error("accepted")
			}
		})
	}
}

// accessTokenExpiringAt returns an access token whose exp claim is expiresAt.
// The CLI never checks its signature.
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

// useAuthProvider points the CLI at an auth provider with no session stored,
// whose token endpoint answers a refresh with a token expiring at
// refreshedExpiresAt.
func useAuthProvider(t *testing.T, refreshedExpiresAt time.Time) *auth.AuthProviderConfig {
	t.Helper()
	return useAuthProviderAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(auth.TokenSet{
			AccessToken:  accessTokenExpiringAt(t, "refreshed", refreshedExpiresAt),
			RefreshToken: "the-next-refresh-token",
			TokenType:    "bearer",
		})
	})
}

// useAuthProviderAt points the CLI at an auth provider with no session stored,
// whose token endpoint is tokenEndpoint.
func useAuthProviderAt(t *testing.T, tokenEndpoint http.HandlerFunc) *auth.AuthProviderConfig {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows

	endpoint := httptest.NewServer(tokenEndpoint)
	t.Cleanup(endpoint.Close)

	providerFile := filepath.Join(t.TempDir(), "provider.yaml")
	providerYAML := "name: Test Auth\nclientId: test-client-id\n" +
		"authEndpoint: " + endpoint.URL + "/oauth2/auth\n" +
		"tokenEndpoint: " + endpoint.URL + "/oauth2/token\n" +
		"revokeEndpoint: " + endpoint.URL + "/oauth2/revoke\n" +
		"userInfoEndpoint: " + endpoint.URL + "/userinfo\n"
	if err := os.WriteFile(providerFile, []byte(providerYAML), 0600); err != nil {
		t.Fatalf("failed to write the provider file: %v", err)
	}
	t.Setenv(auth.AuthProviderFileEnvVar, providerFile)

	provider, err := auth.NewDefaultAuthProvider()
	if err != nil {
		t.Fatalf("NewDefaultAuthProvider: %v", err)
	}
	return provider
}

// logIn stores a human user's session with provider, whose access token
// expires at expiresAt.
func logIn(t *testing.T, provider *auth.AuthProviderConfig, expiresAt time.Time) {
	t.Helper()
	tokenSet := &auth.TokenSet{
		AccessToken:  accessTokenExpiringAt(t, "stored", expiresAt),
		RefreshToken: "a-refresh-token",
		TokenType:    "bearer",
	}
	if err := auth.SaveSessionState(provider, auth.UserTypeHuman, tokenSet); err != nil {
		t.Fatalf("failed to store a session: %v", err)
	}
}

// runProxyPlugin runs the plugin in dir the way kubectl does for a kubeconfig
// pointing at the Kubernetes API proxy, and returns its stdout and stderr.
func runProxyPlugin(t *testing.T, dir string) (string, string, error) {
	t.Helper()
	t.Chdir(dir)

	stdout, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatalf("failed to create stdout: %v", err)
	}
	defer func() { _ = stdout.Close() }()
	// The loggers as initLogger sets them up, the log writing to stdout, so a
	// log line the plugin lets through lands among its output.
	var stderr bytes.Buffer
	previousLogger, previousStderrLogger := log.Logger, stderrLogger
	log.Logger = zerolog.New(&coloredLineConsoleWriter{Out: stdout}).Level(zerolog.InfoLevel)
	stderrLogger = zerolog.New(&stderr).Level(zerolog.InfoLevel)
	t.Cleanup(func() { log.Logger, stderrLogger = previousLogger, previousStderrLogger })

	cmd := &cobra.Command{}
	cmd.SetOut(stdout)
	o := getKubernetesExecCredentialOpts{argEnvironmentHumanID: "tiny-squids", flagProxy: true}
	runErr := o.Run(cmd)

	printed, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}
	return string(printed), stderr.String(), runErr
}

// decodeExecCredential decodes the plugin's stdout, which must be the exec
// credential and nothing else.
func decodeExecCredential(t *testing.T, stdout string) *clientauthenticationv1beta1.ExecCredentialStatus {
	t.Helper()
	var credential clientauthenticationv1beta1.ExecCredential
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(&credential); err != nil || credential.Status == nil {
		t.Fatalf("stdout is not an exec credential: %v\n%s", err, stdout)
	}
	if rest := strings.TrimSpace(stdout[decoder.InputOffset():]); rest != "" {
		t.Errorf("stdout carries more than the exec credential: %q", rest)
	}
	if credential.APIVersion != "client.authentication.k8s.io/v1beta1" || credential.Kind != "ExecCredential" {
		t.Errorf("apiVersion %q, kind %q", credential.APIVersion, credential.Kind)
	}
	return credential.Status
}

// A token expiring within the skew is refreshed before it is handed to kubectl,
// and kubectl is told the refreshed token expires a skew early.
func TestGetKubernetesExecCredential_ProxyRefreshesATokenExpiringWithinTheSkew(t *testing.T) {
	refreshedExpiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	provider := useAuthProvider(t, refreshedExpiresAt)
	logIn(t, provider, time.Now().Add(30*time.Second))

	stdout, _, err := runProxyPlugin(t, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	status := decodeExecCredential(t, stdout)
	if status.Token != accessTokenExpiringAt(t, "refreshed", refreshedExpiresAt) {
		t.Errorf("kubectl was handed the stored token, which expires within the skew")
	}
	if want := refreshedExpiresAt.Add(-envapi.ProxyExecCredentialSkew); status.ExpirationTimestamp == nil || !status.ExpirationTimestamp.Time.Equal(want) {
		t.Errorf("expirationTimestamp = %v, want %v", status.ExpirationTimestamp, want)
	}
}

func TestGetKubernetesExecCredential_ProxyHandsOnATokenValidForLonger(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	provider := useAuthProvider(t, time.Now().Add(time.Hour))
	logIn(t, provider, expiresAt)

	stdout, _, err := runProxyPlugin(t, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	status := decodeExecCredential(t, stdout)
	if status.Token != accessTokenExpiringAt(t, "stored", expiresAt) {
		t.Errorf("kubectl was handed another token than the stored one")
	}
	if want := expiresAt.Add(-envapi.ProxyExecCredentialSkew); status.ExpirationTimestamp == nil || !status.ExpirationTimestamp.Time.Equal(want) {
		t.Errorf("expirationTimestamp = %v, want %v", status.ExpirationTimestamp, want)
	}
}

// kubectl runs the plugin without a terminal, so it cannot log in, and says
// how to instead.
func TestGetKubernetesExecCredential_ProxySaysHowToLogInWithoutASession(t *testing.T) {
	useAuthProvider(t, time.Now().Add(time.Hour))

	stdout, _, err := runProxyPlugin(t, t.TempDir())
	if err == nil {
		t.Fatalf("answered with no session: %s", stdout)
	}
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	if !strings.Contains(cliErr.Suggestion, "metaplay auth login") {
		t.Errorf("suggestion = %q, want it to say how to log in", cliErr.Suggestion)
	}
}

// kubectl runs the plugin wherever the user is, which may be inside a project
// that does not know the environment. The proxy's credential does not depend on
// the project, so it is not resolved.
func TestGetKubernetesExecCredential_ProxyIgnoresTheProjectItRunsIn(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	provider := useAuthProvider(t, time.Now().Add(time.Hour))
	logIn(t, provider, expiresAt)
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "metaplay-project.yaml"), []byte("projectID: another-project\n"), 0600); err != nil {
		t.Fatalf("failed to write the project: %v", err)
	}

	stdout, _, err := runProxyPlugin(t, projectDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	status := decodeExecCredential(t, stdout)
	if status.Token != accessTokenExpiringAt(t, "stored", expiresAt) {
		t.Errorf("kubectl was handed another token than the stored one")
	}
}

// A token the endpoint could not refresh still works until it expires, so it
// is handed on, with its own expiry: the skew's is already past. What went
// wrong is said on stderr, and stdout still carries the credential alone.
func TestGetKubernetesExecCredential_ProxyHandsOnATokenItCouldNotRefresh(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Second).Truncate(time.Second)
	provider := useAuthProviderAt(t, func(w http.ResponseWriter, r *http.Request) {
		// Not retried, unlike the other server errors, so the test need not wait.
		http.Error(w, "down for maintenance", http.StatusNotImplemented)
	})
	logIn(t, provider, expiresAt)

	stdout, stderr, err := runProxyPlugin(t, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	status := decodeExecCredential(t, stdout)
	if status.Token != accessTokenExpiringAt(t, "stored", expiresAt) {
		t.Errorf("kubectl was handed another token than the stored one")
	}
	if status.ExpirationTimestamp == nil || !status.ExpirationTimestamp.Time.Equal(expiresAt) {
		t.Errorf("expirationTimestamp = %v, want the token's own %v", status.ExpirationTimestamp, expiresAt)
	}
	if !strings.Contains(stderr, "down for maintenance") || !strings.Contains(stderr, "Could not refresh") {
		t.Errorf("stderr does not say the refresh failed:\n%s", stderr)
	}
}

// Tokens that live no longer than the skew are reported with their own
// expiry, rather than one already past.
func TestGetKubernetesExecCredential_ProxyReportsAShortLivedTokensOwnExpiry(t *testing.T) {
	refreshedExpiresAt := time.Now().Add(30 * time.Second).Truncate(time.Second)
	provider := useAuthProvider(t, refreshedExpiresAt)
	logIn(t, provider, time.Now().Add(10*time.Second))

	stdout, _, err := runProxyPlugin(t, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	status := decodeExecCredential(t, stdout)
	if status.Token != accessTokenExpiringAt(t, "refreshed", refreshedExpiresAt) {
		t.Errorf("kubectl was handed the stored token, which expires within the skew")
	}
	if status.ExpirationTimestamp == nil || !status.ExpirationTimestamp.Time.Equal(refreshedExpiresAt) {
		t.Errorf("expirationTimestamp = %v, want the token's own %v", status.ExpirationTimestamp, refreshedExpiresAt)
	}
}
