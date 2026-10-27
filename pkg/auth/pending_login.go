/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"encoding/base64"
	"fmt"
	"runtime"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// How long a login started with 'auth login --no-browser' can be finished. Matches
// how long Metaplay Auth keeps the login request and the authorization code alive.
const pendingLoginLifetime = 30 * time.Minute

// A login started without a browser, waiting for the user to paste the code the
// sign-in page showed. Persisted because the CLI that finishes it may be a later
// invocation than the one that started it.
type PersistedPendingLogin struct {
	State         string    `json:"state"`         // OAuth2 state the authorization request carried
	VerifierGCM   string    `json:"verifierGcm"`   // GCM-encrypted PKCE code verifier
	RedirectURI   string    `json:"redirectUri"`   // Redirect URI the code was issued for, needed again in the exchange
	ProviderPrint string    `json:"providerPrint"` // AuthProviderConfig.Fingerprint() of the provider the login was started with
	ExpiresAt     time.Time `json:"expiresAt"`     // After this, the code can no longer be exchanged
}

// A pending login read back from disk, with its verifier decrypted.
type pendingLogin struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	ExpiresAt    time.Time // Set when read back; savePendingLogin starts a new lifetime
}

// savePendingLogin stores a pending login for the provider, replacing any earlier
// one: only the most recent authorization URL is expected to come back.
func savePendingLogin(authProvider *AuthProviderConfig, login pendingLogin, now time.Time) error {
	login.ExpiresAt = now.Add(pendingLoginLifetime)
	return storePendingLogin(authProvider, login, now, true)
}

// restorePendingLogin puts back a login taken by takePendingLogin whose code could
// not be exchanged, so that a mistyped code can be pasted again. A login started
// since then is kept instead, as it is the one whose URL was printed last.
func restorePendingLogin(authProvider *AuthProviderConfig, login *pendingLogin, now time.Time) error {
	return storePendingLogin(authProvider, *login, now, false)
}

func storePendingLogin(authProvider *AuthProviderConfig, login pendingLogin, now time.Time, replace bool) error {
	unlock, err := lockSessionStore()
	if err != nil {
		return err
	}
	defer unlock()

	key, err := getOrCreateAESKey()
	if err != nil {
		return err
	}

	encryptedVerifier, err := encryptGCM([]byte(login.CodeVerifier), key)
	if err != nil {
		return fmt.Errorf("failed to encrypt the code verifier: %w", err)
	}

	persisted := PersistedPendingLogin{
		State:         login.State,
		VerifierGCM:   base64.StdEncoding.EncodeToString(encryptedVerifier),
		RedirectURI:   login.RedirectURI,
		ProviderPrint: authProvider.Fingerprint(),
		ExpiresAt:     login.ExpiresAt,
	}

	return updatePersistedConfig(func(config *PersistedConfig) error {
		// Logins nobody finished are dropped here, so they do not pile up.
		for sessionID, pending := range config.PendingLogins {
			if now.After(pending.ExpiresAt) {
				delete(config.PendingLogins, sessionID)
			}
		}

		sessionID := authProvider.GetSessionID()
		if _, found := config.PendingLogins[sessionID]; found && !replace {
			return nil
		}
		if config.PendingLogins == nil {
			config.PendingLogins = make(map[string]PersistedPendingLogin)
		}
		config.PendingLogins[sessionID] = persisted
		return nil
	})
}

// takePendingLogin removes and returns the provider's pending login, if the pasted
// state is the one it is waiting for. A login is taken before its code is
// exchanged, so that two invocations cannot both exchange it. A paste that does not
// match leaves the login in place, so a mistyped paste can be retried; an expired
// login is removed whatever was pasted.
func takePendingLogin(authProvider *AuthProviderConfig, state string, now time.Time) (*pendingLogin, error) {
	unlock, err := lockSessionStore()
	if err != nil {
		return nil, err
	}
	defer unlock()

	config, err := loadPersistedConfig()
	if err != nil {
		return nil, err
	}

	startCommand := authProvider.LoginCommand() + " --no-browser"
	sessionID := authProvider.GetSessionID()
	persisted, found := config.PendingLogins[sessionID]
	if !found {
		return nil, clierrors.New("No sign-in is waiting for a code").
			WithSuggestion(fmt.Sprintf("Run '%s' first, then paste the code the sign-in page shows", startCommand))
	}

	if now.After(persisted.ExpiresAt) {
		delete(config.PendingLogins, sessionID)
		if err := savePersistedConfig(config); err != nil {
			return nil, err
		}
		return nil, clierrors.New("The sign-in has expired").
			WithSuggestion(fmt.Sprintf("Run '%s' again", startCommand))
	}

	if persisted.ProviderPrint != authProvider.Fingerprint() {
		return nil, clierrors.Newf("The waiting sign-in was started with a different auth provider than '%s'", authProvider.Name).
			WithSuggestion(fmt.Sprintf("Run '%s' again", startCommand))
	}

	if persisted.State != state {
		return nil, clierrors.New("The code belongs to a different sign-in than the one waiting").
			WithDetails("Each 'auth login --no-browser' shows a new URL, and only the code from the most recent one is accepted").
			WithSuggestion("Open the URL the most recent sign-in printed, and paste the code it shows")
	}

	codeVerifier, err := decryptPendingVerifier(persisted.VerifierGCM)
	if err != nil {
		return nil, err
	}

	delete(config.PendingLogins, sessionID)
	if err := savePersistedConfig(config); err != nil {
		return nil, err
	}

	return &pendingLogin{
		State:        persisted.State,
		CodeVerifier: codeVerifier,
		RedirectURI:  persisted.RedirectURI,
		ExpiresAt:    persisted.ExpiresAt,
	}, nil
}

// decryptPendingVerifier decrypts a stored code verifier. On Linux, it was written
// with the fallback key if no keyring was available, as session tokens are.
func decryptPendingVerifier(verifierGCM string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(verifierGCM)
	if err != nil {
		return "", fmt.Errorf("failed to decode the stored code verifier: %w", err)
	}

	key, err := getAESKey()
	if err != nil {
		return "", fmt.Errorf("failed to read the encryption key: %w", err)
	}

	verifier, err := decryptGCM(encrypted, key)
	if err != nil && runtime.GOOS == "linux" {
		verifier, err = decryptGCM(encrypted, linuxFallbackKey)
	}
	if err != nil {
		return "", fmt.Errorf("failed to decrypt the stored code verifier: %w", err)
	}

	return string(verifier), nil
}
