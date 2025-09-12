/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-resty/resty/v2"
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
		// Verify that both ipv4 and ipv6 are available, using `:port` as addr seems to only check ipv6.
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tryPort))
		if err == nil {
			ipv6Listener, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", tryPort))
			if err == nil {
				ipv6Listener.Close()
				return listener, tryPort, nil
			}
		}
	}
	return nil, 0, fmt.Errorf("no available ports between 5000-5004")
}

func LoginWithBrowser(ctx context.Context, authProvider *AuthProviderConfig) error {
	// Set up a local server on a random port.
	listener, port, err := findAvailableCallbackPort()
	if err != nil {
		return fmt.Errorf("failed to find an available port in range 5000..5004: %v", err)
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
		return fmt.Errorf("timeout during authentication")
	}

	// Shutdown the HTTP server gracefully
	if err := server.Shutdown(ctx); err != nil {
		log.Warn().Msgf("Server shutdown error: %v", err)
	}

	return nil
}

// isTransientError checks if an error is transient and should be retried
func isTransientError(err error, statusCode int) bool {
	if err != nil {
		return true // Network errors are generally transient
	}
	// HTTP status codes that indicate transient failures
	return statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
}

// retryWithBackoff executes a function with exponential backoff retry logic
func retryWithBackoff(operation func() (*http.Response, error), maxRetries int) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, lastErr = operation()

		if lastErr == nil && resp != nil && !isTransientError(nil, resp.StatusCode) {
			return resp, nil
		}

		// If this was the last attempt, don't wait
		if attempt == maxRetries {
			break
		}

		// Log the retry attempt
		if resp != nil {
			log.Warn().Msgf("Token endpoint request failed with status %d, retrying in %v (attempt %d/%d)",
				resp.StatusCode, time.Duration(1<<attempt)*time.Second, attempt+1, maxRetries+1)
		} else {
			log.Warn().Msgf("Token endpoint request failed with error %v, retrying in %v (attempt %d/%d)",
				lastErr, time.Duration(1<<attempt)*time.Second, attempt+1, maxRetries+1)
		}

		// Close the response body if present to avoid resource leaks
		if resp != nil {
			resp.Body.Close()
		}

		// Exponential backoff: 1s, 2s, 4s, 8s...
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}

	if resp != nil && lastErr == nil {
		return resp, nil
	}
	return resp, lastErr
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
	resp, err := retryWithBackoff(func() (*http.Response, error) {
		return http.Post(authProvider.TokenEndpoint, "application/x-www-form-urlencoded", bytes.NewBufferString(params.Encode()))
	}, 3) // Retry up to 3 times (4 total attempts)

	if err != nil {
		return fmt.Errorf("failed to send request to token endpoint after retries: %w", err)
	}
	defer resp.Body.Close()

	// Parse the response, there should be a non-empty body containing the token as JSON
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read token endpoint response: %w", err)
	}

	// Check for HTTP errors.
	// TODO: Check whether other 2xx codes with a token in the body should be expected and accepted
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token endpoint returned an error: %s - %s", resp.Status, string(body))
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
	// Resolve userinfo endpoint (on the portal).
	log.Debug().Msgf("Fetch user info from %s", authProvider.UserInfoEndpoint)

	// Make the request
	var userinfo UserInfoResponse
	resp, err := resty.New().R().
		SetAuthToken(tokenSet.AccessToken). // Set Bearer token for Authorization
		SetResult(&userinfo).               // Unmarshal response into the struct
		Get(authProvider.UserInfoEndpoint)

	if err != nil {
		return nil, fmt.Errorf("Failed to fetch userinfo %w", err)
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

	// Make the HTTP POST request
	resp, err := http.Post(authProvider.TokenEndpoint, "application/x-www-form-urlencoded", bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned an error: %s - %s", resp.Status, string(body))
	}

	// Parse the JSON response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token endpoint response: %w", err)
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
