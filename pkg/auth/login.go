/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/httputil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/pkg/browser"
	"github.com/rs/zerolog/log"
)

// Generate a random string for state and PKCE code_verifier
func generateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// generateCodeVerifierAndChallenge generates a code verifier and challenge for exchanging code from Ory server
func generateCodeVerifierAndChallenge() (verifier, challenge string) {
	// Generate verifier hex string
	verifier, err := generateRandomString(32)
	if err != nil {
		log.Fatal().Msgf("failed to generate random string: %v", err)
	}

	// Create SHA-256 hash of the verifier & encode as base64url
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])

	return verifier, challenge
}

// findAvailableCallbackPort attempts to find an available port within the range 5000-5004
// Note: We try these in reverse order as 5000 is more likely to be used by other systems.
func findAvailableCallbackPort() (net.Listener, int, error) {
	for tryPort := 5004; tryPort >= 5000; tryPort-- {
		// Bind to IPv4 loopback only - maximum compatibility across systems
		// (avoids issues on systems with IPv6 disabled or misconfigured)
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", tryPort))
		if err == nil {
			return listener, tryPort, nil
		}
	}
	return nil, 0, clierrors.New("Failed to start authentication callback server").
		WithDetails("All ports in range 5000-5004 are in use").
		WithSuggestion("Close applications using these ports, or check your firewall settings")
}

func LoginWithBrowser(ctx context.Context, authProvider *AuthProviderConfig) error {
	// Set up a local server on a random port.
	listener, port, err := findAvailableCallbackPort()
	if err != nil {
		return err
	}
	defer listener.Close()

	// Construct redirect URI with the port.
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Generate PKCE values.
	codeVerifier, codeChallenge := generateCodeVerifierAndChallenge()

	// Generate a random state for login.
	state, err := generateRandomString(16)
	if err != nil {
		return fmt.Errorf("failed to generate random state: %v", err)
	}

	// Create a channel to signal server shutdown.
	done := make(chan struct{})

	// Create a new HTTP server.
	var server *http.Server
	server = &http.Server{
		Addr: listener.Addr().String(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// OAuth2 callback handler
			if r.URL.Path == "/callback" {
				query := r.URL.Query()
				if err := query.Get("error"); err != "" {
					errDescription := query.Get("error_description")
					http.Error(w, fmt.Sprintf("Authentication failed: %s\nDescription: %s", err, errDescription), http.StatusBadRequest)
					return
				}

				// Validate OAuth2 state parameter to prevent CSRF attacks
				returnedState := query.Get("state")
				if returnedState != state {
					http.Error(w, "Authentication failed: Invalid state parameter", http.StatusBadRequest)
					return
				}

				code := query.Get("code")
				if code == "" {
					http.Error(w, "Authentication failed: No code received", http.StatusBadRequest)
					return
				}

				// Exchange authorization code for tokenSet
				tokenSet, err := exchangeCodeForTokens(code, codeVerifier, redirectURI, authProvider)
				if err != nil {
					http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
					return
				}

				// Save tokens securely
				err = SaveSessionState(authProvider.GetSessionID(), UserTypeHuman, tokenSet)
				if err != nil {
					http.Error(w, "Failed to save tokens: "+err.Error(), http.StatusInternalServerError)
					return
				}

				fmt.Fprintln(w, "Authentication successful! You can close this window.")

				// Signal that authentication is complete
				close(done)
			}
		}),
	}

	// Start the server in a separate goroutine.
	log.Debug().Msgf("Listening for callback from Metaplay Auth on http://localhost:%d/", port)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Msgf("HTTP server error: %v", err)
		}
	}()

	// Construct authorization URL with proper encoding
	authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&scope=%s&audience=%s&state=%s",
		authProvider.AuthEndpoint,
		authProvider.ClientID,
		url.QueryEscape(redirectURI),
		codeChallenge,
		url.QueryEscape(authProvider.Scopes),
		url.QueryEscape(authProvider.Audience),
		url.QueryEscape(state))

	// Log the authorization URL for manual fallback
	log.Info().Msgf("Opening a browser to log in. If a browser did not open up, you can copy-paste the following URL to authenticate: %s", styles.RenderMuted(authURL))
	err = browser.OpenURL(authURL)
	if err != nil {
		log.Warn().Msgf("Unable to open browser: %v", err)
		log.Info().Msg(styles.RenderAttention("Please open the URL above in your browser."))
	}

	// Wait for authentication to complete or timeout.
	select {
	case <-done:
		log.Info().Msg("")
		log.Info().Msg(styles.RenderSuccess("✅ Authenticated successfully!"))
	case <-time.After(5 * time.Minute):
		return clierrors.New("Authentication timed out after 5 minutes").
			WithSuggestion("Log in again")
	}

	// Shutdown the HTTP server gracefully
	if err := server.Shutdown(ctx); err != nil {
		log.Warn().Msgf("Server shutdown error: %v", err)
	}

	return nil
}

func MachineLogin(authProvider *AuthProviderConfig, clientID, clientSecret string) error {
	// Get a fresh access token from Metaplay Auth.
	params := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {"openid email profile offline_access"},
	}

	// Make the HTTP request to the Metaplay Auth server OAuth2 token endpoint with retry logic
	body, statusCode, err := httputil.PostFormWithRetry(authProvider.TokenEndpoint, params.Encode())
	if err != nil {
		return clierrors.Wrap(err, "Failed to authenticate with Metaplay").
			WithSuggestion("Check your network connection and try again")
	}

	// Check for HTTP errors.
	if statusCode != http.StatusOK {
		return clierrors.Newf("Authentication failed with status %d", statusCode).
			WithDetails(string(body)).
			WithSuggestion("Verify your client ID and secret are correct")
	}

	// Parse a TokenSet object from the response body JSON
	var tokenSet TokenSet
	err = json.Unmarshal(body, &tokenSet)
	if err != nil {
		return fmt.Errorf("failed to parse token JSON: %w", err)
	}

	// Save tokens securely
	err = SaveSessionState(authProvider.GetSessionID(), UserTypeMachine, &tokenSet)
	if err != nil {
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	// Fetch the user info.
	userinfo, err := FetchUserInfo(authProvider, &tokenSet)
	if err != nil {
		return err
	}

	log.Info().Msgf("You are now logged in with machine user %s %s (clientId=%s) and can execute other commands.", userinfo.GivenName, userinfo.FamilyName, userinfo.Subject)

	return nil
}

func FetchUserInfo(authProvider *AuthProviderConfig, tokenSet *TokenSet) (*UserInfoResponse, error) {
	log.Debug().Msgf("Fetch user info from %s", authProvider.UserInfoEndpoint)

	var userinfo UserInfoResponse
	resp, err := httputil.NewRetryClient().R().
		SetAuthToken(tokenSet.AccessToken). // Set Bearer token for Authorization
		SetResult(&userinfo).               // Unmarshal response into the struct
		Get(authProvider.UserInfoEndpoint)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch userinfo: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("API error: %s", resp.Status())
	}

	return &userinfo, nil
}

// Exchange the OAuth2 code for the token set.
func exchangeCodeForTokens(code, verifier, redirectURI string, authProvider *AuthProviderConfig) (*TokenSet, error) {
	// Prepare the POST request payload
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", authProvider.ClientID)
	data.Set("code_verifier", verifier)

	// Make the HTTP POST request with retry logic for transient errors
	// Note: Authorization codes are single-use, but retrying on network errors is still safe
	// since the code won't be consumed if the request never reached the server
	body, statusCode, err := httputil.PostFormWithRetry(authProvider.TokenEndpoint, data.Encode())
	if err != nil {
		return nil, fmt.Errorf("failed to send request to token endpoint: %w", err)
	}

	// Check for HTTP errors
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned an error: %d - %s", statusCode, string(body))
	}
	// log.Debug().Msgf("Response body: %s", body)
	// Sample response:
	// {
	// 	"access_token": "<JWT>",
	// 	"expires_in": 86399,
	// 	"id_token": "<JWT>",
	// 	"refresh_token": "<opaque refresh token>",
	// 	"scope": "openid offline_access",
	// 	"token_type": "bearer"
	// }

	// Unmarshal JSON response into a typed struct
	var tokenSet TokenSet
	err = json.Unmarshal(body, &tokenSet)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token JSON: %w", err)
	}

	// Ensure required tokens are present
	if tokenSet.IDToken == "" {
		return nil, errors.New("response missing id_token")
	}
	if tokenSet.AccessToken == "" {
		return nil, errors.New("response missing access_token")
	}
	if tokenSet.RefreshToken == "" {
		return nil, errors.New("response missing refresh_token")
	}

	return &tokenSet, nil
}
