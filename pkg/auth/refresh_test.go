/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zalando/go-keyring"
)

func TestMergeRefreshedTokenSet(t *testing.T) {
	previous := &TokenSet{
		IDToken:      "old-id-token",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}

	tests := []struct {
		name             string
		refreshed        *TokenSet
		wantIDToken      string
		wantAccessToken  string
		wantRefreshToken string
	}{
		{
			name:             "server returns all fields",
			refreshed:        &TokenSet{IDToken: "new-id-token", AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
			wantIDToken:      "new-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "new-refresh-token",
		},
		{
			name:             "server omits id_token (Ory behavior)",
			refreshed:        &TokenSet{IDToken: "", AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
			wantIDToken:      "old-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "new-refresh-token",
		},
		{
			name:             "server omits refresh_token (rotation disabled)",
			refreshed:        &TokenSet{IDToken: "new-id-token", AccessToken: "new-access-token", RefreshToken: ""},
			wantIDToken:      "new-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "old-refresh-token",
		},
		{
			name:             "server returns only a new access_token",
			refreshed:        &TokenSet{IDToken: "", AccessToken: "new-access-token", RefreshToken: ""},
			wantIDToken:      "old-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "old-refresh-token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRefreshedTokenSet(previous, tc.refreshed)
			if got.IDToken != tc.wantIDToken {
				t.Errorf("IDToken = %q, want %q", got.IDToken, tc.wantIDToken)
			}
			if got.AccessToken != tc.wantAccessToken {
				t.Errorf("AccessToken = %q, want %q", got.AccessToken, tc.wantAccessToken)
			}
			if got.RefreshToken != tc.wantRefreshToken {
				t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, tc.wantRefreshToken)
			}
		})
	}
}

// accessTokenExpiringAt is an access token whose exp claim is expiresAt. Its
// signature is never checked on this side.
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

// providerAt is a provider whose token endpoint is handler.
func providerAt(t *testing.T, handler http.HandlerFunc) *AuthProviderConfig {
	t.Helper()
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)

	return &AuthProviderConfig{
		Name:          "Test Auth",
		ClientID:      "test-client-id",
		TokenEndpoint: endpoint.URL,
	}
}

// rotatingProvider is a provider whose token endpoint rotates the refresh
// token, and counts how often it was asked. A refresh token presented twice
// revokes the session, as Ory does by default.
func rotatingProvider(t *testing.T) (*AuthProviderConfig, *atomic.Int32) {
	t.Helper()
	var (
		refreshes atomic.Int32
		mu        sync.Mutex
		current   = "a-refresh-token"
	)
	return providerAt(t, func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "not a refresh", http.StatusBadRequest)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if current == "" || r.Form.Get("refresh_token") != current {
			current = ""
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		current = "a-refresh-token-" + time.Now().Format(time.RFC3339Nano)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenSet{
			AccessToken:  accessTokenExpiringAt(t, "refreshed", time.Now().Add(time.Hour)),
			RefreshToken: current,
			TokenType:    "bearer",
		})
	}), &refreshes
}

// storeSession signs in to provider as a person, or as a machine user holding
// no refresh token, with an access token expiring at expiresAt.
func storeSession(t *testing.T, provider *AuthProviderConfig, userType UserType, expiresAt time.Time) {
	t.Helper()
	keyring.MockInit()
	redirectConfigHome(t)

	tokenSet := &TokenSet{AccessToken: accessTokenExpiringAt(t, "stored", expiresAt), TokenType: "bearer"}
	if userType == UserTypeHuman {
		tokenSet.RefreshToken = "a-refresh-token"
	}
	if err := SaveSessionState(provider, userType, tokenSet); err != nil {
		t.Fatalf("failed to store a session: %v", err)
	}
}

func subjectOf(t *testing.T, tokenSet *TokenSet) string {
	t.Helper()
	token, _, err := jwt.NewParser().ParseUnverified(tokenSet.AccessToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("not a token: %v", err)
	}
	subject, _ := token.Claims.GetSubject()
	return subject
}

// Processes refreshing one expired session at once take turns: the first
// refreshes, and the rest load its tokens. Presenting the same refresh token
// again would revoke the session. The callers here are goroutines, each opening
// the lock file for itself, which the lock excludes as it does other processes.
func TestLoadAndRefreshTokenSet_RefreshesOnceForConcurrentCallers(t *testing.T) {
	provider, refreshes := rotatingProvider(t)
	storeSession(t, provider, UserTypeHuman, time.Now().Add(-time.Minute))

	const callers = 8
	var wg sync.WaitGroup
	tokenSets := make([]*TokenSet, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			tokenSets[i], errs[i] = LoadAndRefreshTokenSet(provider)
		})
	}
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if got := subjectOf(t, tokenSets[i]); got != "refreshed" {
			t.Errorf("caller %d answered with the %s token, want the refreshed one", i, got)
		}
	}
	if refreshes.Load() != 1 {
		t.Errorf("refreshed %d times, want once", refreshes.Load())
	}
	if stored, err := LoadSessionState(provider); err != nil || stored == nil {
		t.Errorf("the session is gone: %v", err)
	}
}

// Only a refused grant signs the user out. Any other failure keeps the session
// for the next attempt. The server errors the CLI retries (500, 502-504) take
// seconds to exhaust, so 501 stands in for them.
func TestLoadAndRefreshTokenSet_KeepsTheSessionUnlessTheGrantIsRefused(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantKept bool
	}{
		{"invalid grant", http.StatusBadRequest, false},
		{"client refused", http.StatusUnauthorized, false},
		{"forbidden in between", http.StatusForbidden, true},
		{"server error", http.StatusNotImplemented, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := providerAt(t, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "no", test.status)
			})
			storeSession(t, provider, UserTypeHuman, time.Now().Add(-time.Minute))

			if _, err := LoadAndRefreshTokenSet(provider); err == nil {
				t.Fatal("answered with an expired token")
			}

			stored, err := LoadSessionState(provider)
			if err != nil {
				t.Fatalf("LoadSessionState: %v", err)
			}
			if kept := stored != nil; kept != test.wantKept {
				t.Errorf("session kept = %v, want %v", kept, test.wantKept)
			}
		})
	}
}
