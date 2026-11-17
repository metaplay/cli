/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	clierrors "github.com/metaplay/cli/internal/errors"
)

func TestParsePastedCode(t *testing.T) {
	tests := []struct {
		name      string
		pasted    string
		wantState string
		wantCode  string
		wantErr   bool
	}{
		{"state and code", "0123abcd.ory_ac_abc.def", "0123abcd", "ory_ac_abc.def", false},
		{"surrounding whitespace and newline", "  0123abcd.ory_ac_abc.def\n", "0123abcd", "ory_ac_abc.def", false},
		{"quoted", "'0123abcd.ory_ac_abc.def'", "0123abcd", "ory_ac_abc.def", false},
		{"double-quoted", `"0123abcd.ory_ac_abc.def"`, "0123abcd", "ory_ac_abc.def", false},
		{"Windows line ending", "0123abcd.ory_ac_abc.def\r\n", "0123abcd", "ory_ac_abc.def", false},
		{"redirect URL", "https://auth-ui.example.com/cli/code?code=ory_ac_abc.def&scope=openid&state=0123abcd", "0123abcd", "ory_ac_abc.def", false},
		{"redirect URL with an error", "https://auth-ui.example.com/cli/code?error=access_denied&state=0123abcd", "", "", true},
		{"redirect URL without a code", "https://auth-ui.example.com/cli/code?state=0123abcd", "", "", true},
		{"empty", "  ", "", "", true},
		{"code alone", "ory_ac_abc", "", "", true},
		{"no state", ".ory_ac_abc.def", "", "", true},
		{"no code", "0123abcd.", "", "", true},
		{"inner whitespace", "0123abcd.ory_ac abc", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, code, err := parsePastedCode(tt.pasted)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePastedCode(%q) error = %v, wantErr %v", tt.pasted, err, tt.wantErr)
			}
			if state != tt.wantState || code != tt.wantCode {
				t.Errorf("parsePastedCode(%q) = (%q, %q), want (%q, %q)", tt.pasted, state, code, tt.wantState, tt.wantCode)
			}
		})
	}
}

// noBrowserProvider is a provider whose token and userinfo endpoints are served by
// a test server answering for one code.
func noBrowserProvider(t *testing.T, wantCode, wantVerifier string) *AuthProviderConfig {
	t.Helper()

	const redirectURI = "https://auth-ui.example.com/cli/code"
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse the token request: %v", err)
		}
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != wantCode ||
			r.PostForm.Get("code_verifier") != wantVerifier ||
			r.PostForm.Get("redirect_uri") != redirectURI {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","id_token":"id","refresh_token":"refresh","expires_in":3600,"token_type":"bearer"}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"abc","name":"Ada Lovelace","email":"ada@example.com"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &AuthProviderConfig{
		Name:                 "Example Platform",
		ClientID:             "example-client",
		AuthEndpoint:         server.URL + "/oauth2/auth",
		TokenEndpoint:        server.URL + "/oauth2/token",
		UserInfoEndpoint:     server.URL + "/userinfo",
		NoBrowserRedirectURI: redirectURI,
	}
}

func startPendingLogin(t *testing.T, provider *AuthProviderConfig, state, verifier string, now time.Time) {
	t.Helper()

	err := savePendingLogin(provider, pendingLogin{
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  provider.NoBrowserRedirectURI,
	}, now)
	if err != nil {
		t.Fatalf("savePendingLogin() error = %v", err)
	}
}

func pendingLoginCount(t *testing.T) int {
	t.Helper()

	config, err := loadPersistedConfig()
	if err != nil {
		t.Fatalf("loadPersistedConfig() error = %v", err)
	}
	return len(config.PendingLogins)
}

// A pasted code is exchanged with the verifier and redirect URI the login started
// with, the session is saved, and the login cannot be finished twice.
func TestCompleteLoginWithCode(t *testing.T) {
	keyring.MockInit()
	redirectConfigHome(t)
	provider := noBrowserProvider(t, "ory_ac_abc.def", "the-verifier")
	startPendingLogin(t, provider, "0123abcd", "the-verifier", time.Now())

	if err := CompleteLoginWithCode(provider, "0123abcd.ory_ac_abc.def\n"); err != nil {
		t.Fatalf("CompleteLoginWithCode() error = %v", err)
	}

	session, err := LoadSessionState(provider)
	if err != nil {
		t.Fatalf("LoadSessionState() error = %v", err)
	}
	if session == nil || session.UserType != UserTypeHuman || session.TokenSet.AccessToken != "access" {
		t.Errorf("session = %+v, want a human session with the exchanged tokens", session)
	}
	if n := pendingLoginCount(t); n != 0 {
		t.Errorf("%d pending logins left, want none", n)
	}

	err = CompleteLoginWithCode(provider, "0123abcd.ory_ac_abc.def")
	if err == nil || !strings.Contains(err.Error(), "No sign-in is waiting") {
		t.Errorf("second CompleteLoginWithCode() error = %v, want no waiting sign-in", err)
	}
}

// A code that the provider refuses leaves the login waiting, so the code can be
// pasted again, as can one that was cut short when copied.
func TestCompleteLoginWithCodeKeepsLoginWhenExchangeFails(t *testing.T) {
	keyring.MockInit()
	redirectConfigHome(t)
	provider := noBrowserProvider(t, "ory_ac_abc.def", "the-verifier")
	startPendingLogin(t, provider, "0123abcd", "the-verifier", time.Now())

	if err := CompleteLoginWithCode(provider, "0123abcd.ory_ac_abc.de"); err == nil {
		t.Fatal("CompleteLoginWithCode() accepted a code the provider refused")
	}
	if n := pendingLoginCount(t); n != 1 {
		t.Fatalf("%d pending logins left, want the login still waiting", n)
	}
	if err := CompleteLoginWithCode(provider, "0123abcd.ory_ac_abc.def"); err != nil {
		t.Fatalf("CompleteLoginWithCode() error = %v on the second paste", err)
	}
}

// An empty code, such as from an unset shell variable, is refused.
func TestCompleteLoginWithEmptyCode(t *testing.T) {
	keyring.MockInit()
	redirectConfigHome(t)
	provider := noBrowserProvider(t, "", "")

	err := CompleteLoginWithCode(provider, "")
	if err == nil || !strings.Contains(err.Error(), "No code was given") {
		t.Errorf("CompleteLoginWithCode(\"\") error = %v, want no code given", err)
	}
}

func TestTakePendingLogin(t *testing.T) {
	now := time.Now()

	t.Run("another login's code leaves the login waiting", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		provider := noBrowserProvider(t, "", "")
		startPendingLogin(t, provider, "0123abcd", "the-verifier", now)

		if _, err := takePendingLogin(provider, "ffffffff", now); err == nil {
			t.Fatal("takePendingLogin() accepted another login's state")
		}
		login, err := takePendingLogin(provider, "0123abcd", now)
		if err != nil {
			t.Fatalf("takePendingLogin() error = %v after a mismatch", err)
		}
		if login.CodeVerifier != "the-verifier" || login.RedirectURI != provider.NoBrowserRedirectURI {
			t.Errorf("login = %+v", login)
		}
	})

	t.Run("an expired login is removed", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		provider := noBrowserProvider(t, "", "")
		startPendingLogin(t, provider, "0123abcd", "the-verifier", now.Add(-pendingLoginLifetime-time.Minute))

		_, err := takePendingLogin(provider, "0123abcd", now)
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Errorf("takePendingLogin() error = %v, want expired", err)
		}
		if n := pendingLoginCount(t); n != 0 {
			t.Errorf("%d pending logins left, want none", n)
		}
	})

	t.Run("starting a login drops other expired logins", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		provider := noBrowserProvider(t, "", "")
		other := *provider
		other.Name = "Other Platform"
		startPendingLogin(t, &other, "0123abcd", "the-verifier", now.Add(-pendingLoginLifetime-time.Minute))

		startPendingLogin(t, provider, "4567cdef", "the-verifier", now)
		if n := pendingLoginCount(t); n != 1 {
			t.Errorf("%d pending logins, want only the one just started", n)
		}
	})

	t.Run("a login started with another provider sharing the session is refused", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		provider := noBrowserProvider(t, "", "")
		startPendingLogin(t, provider, "0123abcd", "the-verifier", now)

		other := *provider
		other.TokenEndpoint = "https://auth.other.example.com/oauth2/token"
		if _, err := takePendingLogin(&other, "0123abcd", now); err == nil {
			t.Error("takePendingLogin() accepted a login started with another provider")
		}
	})
}

func TestLoginWithoutBrowser(t *testing.T) {
	t.Run("without a terminal, the login is left waiting", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		provider := noBrowserProvider(t, "", "")

		if err := LoginWithoutBrowser(provider, nil); err != nil {
			t.Fatalf("LoginWithoutBrowser() error = %v", err)
		}

		config, err := loadPersistedConfig()
		if err != nil {
			t.Fatalf("loadPersistedConfig() error = %v", err)
		}
		pending, found := config.PendingLogins[provider.GetSessionID()]
		if !found {
			t.Fatal("no pending login was saved")
		}
		if pending.RedirectURI != provider.NoBrowserRedirectURI || pending.State == "" || pending.VerifierGCM == "" {
			t.Errorf("pending login = %+v", pending)
		}
	})

	t.Run("a pasted code finishes the login", func(t *testing.T) {
		keyring.MockInit()
		redirectConfigHome(t)
		// The code verifier is generated inside, so the token endpoint cannot check
		// it here; TestCompleteLoginWithCode does.
		provider := noBrowserProvider(t, "ory_ac_abc.def", "")
		provider.TokenEndpoint = noBrowserTokenEndpointAcceptingAnyVerifier(t)

		// The state is generated inside too: answer with whatever the login saved.
		input := &stateEchoReader{t: t, provider: provider, code: "ory_ac_abc.def"}
		if err := LoginWithoutBrowser(provider, input); err != nil {
			t.Fatalf("LoginWithoutBrowser() error = %v", err)
		}
		if session, err := LoadSessionState(provider); err != nil || session == nil {
			t.Errorf("LoadSessionState() = %+v, %v, want a session", session, err)
		}
	})

	t.Run("a provider without a code page is refused", func(t *testing.T) {
		provider := &AuthProviderConfig{Name: "Example Platform"}
		if err := LoginWithoutBrowser(provider, nil); err == nil {
			t.Error("LoginWithoutBrowser() accepted a provider without noBrowserRedirectUri")
		}
	})
}

// noBrowserTokenEndpointAcceptingAnyVerifier serves a token endpoint that answers
// any authorization code exchange.
func noBrowserTokenEndpointAcceptingAnyVerifier(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","id_token":"id","refresh_token":"refresh","expires_in":3600,"token_type":"bearer"}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// stateEchoReader plays the user pasting the code: when read, it answers with
// the state of the login waiting on disk and the given code.
type stateEchoReader struct {
	t        *testing.T
	provider *AuthProviderConfig
	code     string
	done     bool
}

func (r *stateEchoReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, nil
	}
	r.done = true

	config, err := loadPersistedConfig()
	if err != nil {
		r.t.Fatalf("loadPersistedConfig() error = %v", err)
	}
	pending := config.PendingLogins[r.provider.GetSessionID()]
	return copy(p, pending.State+"."+r.code+"\n"), nil
}

// Without a terminal, a browser that cannot be opened fails the login at once and
// points at --no-browser, rather than waiting for a callback nobody can send.
func TestLoginWithBrowserFailsFastWithoutTerminal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("browser launchers are found on PATH only on Linux")
	}
	t.Setenv("PATH", t.TempDir())
	provider := noBrowserProvider(t, "", "")

	done := make(chan error, 1)
	go func() { done <- LoginWithBrowser(context.Background(), provider, false) }()
	select {
	case err := <-done:
		cliErr, ok := clierrors.AsCLIError(err)
		if !ok || !strings.Contains(cliErr.Suggestion, "--no-browser") {
			t.Errorf("LoginWithBrowser() error = %v, want a suggestion of --no-browser", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("LoginWithBrowser() waited for a callback instead of failing")
	}
}
