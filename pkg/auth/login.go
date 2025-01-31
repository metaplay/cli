/*
 * Copyright Metaplay. All rights reserved.
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
	"github.com/metaplay/cli/pkg/common"
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
func findAvailableCallbackPort() (net.Listener, int, error) {
	for tryPort := 5000; tryPort <= 5004; tryPort++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", tryPort))
		if err == nil {
			return listener, tryPort, nil
		}
	}
	return nil, 0, fmt.Errorf("no available ports between 5000-5004")
}

func LoginWithBrowser(ctx context.Context) error {
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
					http.Error(w, "Authentication failed: "+err, http.StatusBadRequest)
					return
				}

				code := query.Get("code")
				if code == "" {
					http.Error(w, "Authentication failed: No code received", http.StatusBadRequest)
					return
				}

				// Exchange authorization code for tokenSet
				tokenSet, err := exchangeCodeForTokens(code, codeVerifier, redirectURI)
				if err != nil {
					http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
					return
				}

				// Save tokens securely
				err = SaveTokenSet(tokenSet)
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
	const authorizationUrl = `${authorizationEndpoint}?response_type=code&client_id=${clientId}&redirect_uri=${encodeURIComponent(redirectUri)}&code_challenge=${challenge}&code_challenge_method=S256&scope=${encodeURIComponent('openid offline_access')}&state=${encodeURIComponent(state)}`
	authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&scope=%s&state=%s",
		authEndpoint,
		clientID,
		url.QueryEscape(redirectURI),
		codeChallenge,
		url.QueryEscape("openid offline_access"),
		url.QueryEscape(state))

	// Log the authorization URL for manual fallback
	log.Info().Msgf("Opening a browser to log in. If a browser did not open up, you can copy-paste the following URL to authenticate: %s", styles.RenderMuted(authURL))
	browser.OpenURL(authURL)

	// Wait for authentication to complete or timeout.
	select {
	case <-done:
		log.Info().Msg("")
		log.Info().Msg(styles.RenderSuccess("✅ Authentication successful!"))
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timeout during authentication")
	}

	// Shutdown the HTTP server gracefully
	if err := server.Shutdown(ctx); err != nil {
		log.Warn().Msgf("Server shutdown error: %v", err)
	}

	return nil
}

func MachineLogin(clientId, clientSecret string) error {
	// Get a fresh access token from Metaplay Auth.
	params := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientId},
		"client_secret": {clientSecret},
		"scope":         {"openid email profile offline_access"},
	}

	// Make the HTTP request to the Metaplay Auth server OAuth2 token endpoint
	resp, err := http.Post(tokenEndpoint, "application/x-www-form-urlencoded", bytes.NewBufferString(params.Encode()))
	if err != nil {
		return fmt.Errorf("Failed to send request to token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Parse the response, there should be a non-empty body containing the token as JSON
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Failed to read token endpoint response: %w", err)
	}

	// Check for HTTP errors
	// TODO: Check whether other 2xx codes with a token in the body should be expected and accepted
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Token endpoint returned an error: %s - %s", resp.Status, string(body))
	}

	// Parse a TokenSet object from the response body JSON
	var tokenSet TokenSet
	err = json.Unmarshal(body, &tokenSet)
	if err != nil {
		return fmt.Errorf("Failed to parse token JSON: %w", err)
	}

	// Save tokens securely
	err = SaveTokenSet(&tokenSet)
	if err != nil {
		return fmt.Errorf("Failed to save tokens: %w", err)
	}

	// TODO: There is probably unnecessary repetition in the error formatting
	userinfo, err := FetchUserInfo(&tokenSet)
	if err != nil {
		return fmt.Errorf("Failed to fetch userinfo: %w", err)
	}

	log.Info().Msgf("You are now logged in with machine user %s %s (clientId=%s) and can execute other commands.", userinfo.GivenName, userinfo.FamilyName, userinfo.Subject)

	return nil
}

func FetchUserInfo(tokenSet *TokenSet) (*UserInfoResponse, error) {
	// Resolve userinfo endpoint (on the portal).
	userInfoEndpoint := common.PortalBaseURL + "/api/external/userinfo"
	log.Debug().Msgf("Fetch user info from %s", userInfoEndpoint)

	// Make the request
	var userinfo UserInfoResponse
	resp, err := resty.New().R().
		SetAuthToken(tokenSet.AccessToken). // Set Bearer token for Authorization
		SetResult(&userinfo).               // Unmarshal response into the struct
		Get(userInfoEndpoint)

	if err != nil {
		return nil, fmt.Errorf("Failed to fetch userinfo %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("API error: %s", resp.Status())
	}

	return &userinfo, nil
}

// Exchange the OAuth2 code for the token set.
func exchangeCodeForTokens(code, verifier, redirectURI string) (*TokenSet, error) {
	// Prepare the POST request payload
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", clientID)
	data.Set("code_verifier", verifier)

	// Make the HTTP POST request
	resp, err := http.Post(tokenEndpoint, "application/x-www-form-urlencoded", bytes.NewBufferString(data.Encode()))
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
