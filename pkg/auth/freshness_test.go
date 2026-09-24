/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// refreshingProvider is a provider whose token endpoint answers every refresh
// with a token good for an hour, and counts how often it was asked.
func refreshingProvider(t *testing.T) (*AuthProviderConfig, *atomic.Int32) {
	t.Helper()
	var refreshes atomic.Int32
	return providerAt(t, func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "not a refresh", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenSet{
			AccessToken:  accessTokenExpiringAt(t, "refreshed", time.Now().Add(time.Hour)),
			RefreshToken: "the-next-refresh-token",
			TokenType:    "bearer",
		})
	}), &refreshes
}

// A token that will have expired before a margin is up is refreshed now, and
// the refreshed one is what the session keeps.
func TestLoadAndRefreshTokenSetWithin_RefreshesATokenExpiringWithinTheMargin(t *testing.T) {
	provider, refreshes := refreshingProvider(t)
	storeSession(t, provider, UserTypeHuman, time.Now().Add(30*time.Second))

	tokenSet, err := LoadAndRefreshTokenSetWithin(provider, time.Minute)
	if err != nil {
		t.Fatalf("LoadAndRefreshTokenSetWithin: %v", err)
	}

	if refreshes.Load() != 1 {
		t.Errorf("refreshed %d times, want once", refreshes.Load())
	}
	if got := subjectOf(t, tokenSet); got != "refreshed" {
		t.Errorf("answered with the %s token, want the refreshed one", got)
	}
	stored, err := LoadSessionState(provider)
	if err != nil || stored == nil {
		t.Fatalf("the session is gone: %v", err)
	}
	if got := subjectOf(t, stored.TokenSet); got != "refreshed" {
		t.Errorf("the session keeps the %s token, want the refreshed one", got)
	}
}

// One good for longer than the margin is left alone: refreshing on every
// invocation would spend a refresh token each time kubectl asks.
func TestLoadAndRefreshTokenSetWithin_KeepsATokenGoodForLongerThanTheMargin(t *testing.T) {
	provider, refreshes := refreshingProvider(t)
	storeSession(t, provider, UserTypeHuman, time.Now().Add(5*time.Minute))

	tokenSet, err := LoadAndRefreshTokenSetWithin(provider, time.Minute)
	if err != nil {
		t.Fatalf("LoadAndRefreshTokenSetWithin: %v", err)
	}

	if refreshes.Load() != 0 {
		t.Errorf("refreshed %d times, want none", refreshes.Load())
	}
	if got := subjectOf(t, tokenSet); got != "stored" {
		t.Errorf("answered with the %s token, want the stored one", got)
	}
}

// The control: everything else refreshes once a token has expired, and not a
// moment before, as it always has.
func TestLoadAndRefreshTokenSet_StillWaitsForATokenToExpire(t *testing.T) {
	provider, refreshes := refreshingProvider(t)
	storeSession(t, provider, UserTypeHuman, time.Now().Add(30*time.Second))

	if _, err := LoadAndRefreshTokenSet(provider); err != nil {
		t.Fatalf("LoadAndRefreshTokenSet: %v", err)
	}

	if refreshes.Load() != 0 {
		t.Errorf("refreshed %d times, want none", refreshes.Load())
	}
}

// A machine user holds no refresh token, so a token expiring within the margin
// cannot be replaced, and handing it on would fail within the minute. Refused,
// saying how to get another.
func TestLoadAndRefreshTokenSetWithin_RefusesAMachineTokenItCannotRefresh(t *testing.T) {
	provider, refreshes := refreshingProvider(t)
	storeSession(t, provider, UserTypeMachine, time.Now().Add(30*time.Second))

	_, err := LoadAndRefreshTokenSetWithin(provider, time.Minute)
	if err == nil {
		t.Fatal("answered with a machine token expiring within the margin")
	}
	if refreshes.Load() != 0 {
		t.Errorf("tried to refresh a session with no refresh token")
	}
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a CLIError: %v", err)
	}
	// Not yet expired, and not reported as if it had.
	if !strings.Contains(cliErr.Message, "expires within") {
		t.Errorf("message = %q, want it to say the token expires within the margin", cliErr.Message)
	}
	if !strings.Contains(cliErr.Suggestion, "machine-login") {
		t.Errorf("suggestion = %q, want it to say how to get another token", cliErr.Suggestion)
	}
}
