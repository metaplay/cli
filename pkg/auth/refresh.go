/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

// Get the expires-at of the access token of the tokenSet.
func getAccessTokenExpiresAt(tokenSet *TokenSet) (time.Time, error) {
	// Parse the token without validation
	token, _, err := jwt.NewParser().ParseUnverified(tokenSet.AccessToken, jwt.MapClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse token: %w", err)
	}

	// Extract claims
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		// Check for the "exp" claim
		if exp, ok := claims["exp"].(float64); ok {
			// Convert Unix timestamp to time.Time
			return time.Unix(int64(exp), 0), nil
		}
		return time.Time{}, fmt.Errorf("token does not contain an 'exp' claim")
	}

	return time.Time{}, fmt.Errorf("failed to parse claims")
}

// Load the current token set. If not logged in, just return empty tokens.
// If logged in and tokens have expired, refresh the tokens. If the refresh
// fails, return an error.
// \todo Forget the tokens if the refresh fails (due to keys already used)
func LoadAndRefreshTokenSet(authProvider *AuthProviderConfig) (*TokenSet, error) {
	// Get current session (including credentials).
	sessionState, err := LoadSessionState(authProvider.GetSessionID())
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}

	// If no tokens, user is not logged in; return empty token set.
	if sessionState == nil {
		return nil, nil
	}

	// Resolve when access token expires.
	tokenSet := sessionState.TokenSet
	expiresAt, err := getAccessTokenExpiresAt(tokenSet)

	// Compare expiration time with the current time
	isExpired := time.Now().After(expiresAt)

	// Refresh the tokenSet (if we have a refresh token -- machine users do not).
	if isExpired {
		if tokenSet.RefreshToken != "" {
			// Refresh the tokenSet.
			tokenSet, err = refreshTokenSet(tokenSet, authProvider)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh tokens: %w", err)
			}

			// Persist the refreshed tokens.
			err = SaveSessionState(authProvider.GetSessionID(), sessionState.UserType, tokenSet)
			if err != nil {
				return nil, fmt.Errorf("failed to persist refreshed tokens: %w", err)
			}
		} else {
			return nil, fmt.Errorf("access token has expired and there is no refresh token")
		}
	}

	return tokenSet, nil
}

// Refresh the tokenSet. Return a new tokenSet that was returned by the token endpoint.
func refreshTokenSet(tokenSet *TokenSet, authProvider *AuthProviderConfig) (*TokenSet, error) {
	// Create URL-encoded form data
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tokenSet.RefreshToken)
	data.Set("scope", authProvider.Scopes) //"openid offline_access")
	data.Set("client_id", authProvider.ClientID)

	// Prepare the POST request
	req, err := http.NewRequest("POST", authProvider.TokenEndpoint, bytes.NewBufferString(data.Encode()))
	if err != nil {
		log.Error().Msgf("Failed to create HTTP request: %v", err)
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Error().Msgf("Failed to refresh tokens via endpoint %s: %v", authProvider.TokenEndpoint, err)
		if err.Error() == "x509: certificate signed by unknown authority" {
			return nil, errors.New("failed to refresh tokens: SSL certificate validation failed. Is someone tampering with your internet connection?")
		}
		return nil, fmt.Errorf("failed to refresh tokens via %s: %w", authProvider.TokenEndpoint, err)
	}
	defer resp.Body.Close()

	// Check for a non-OK response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error().Msgf("Failed to refresh tokens. Response: %s", body)
		log.Debug().Msg("Clearing local credentials...")

		// Remove the session state (something has gone badly wrong).
		err = DeleteSessionState(authProvider.GetSessionID())
		if err != nil {
			return nil, fmt.Errorf("failed to delete bad tokens: %w", err)
		}

		log.Debug().Msg("Local credentials removed.")
		return nil, errors.New("failed to refresh tokens, exiting. Please log in again")
	}

	// Parse the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Msgf("Failed to read response body: %v", err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var tokens TokenSet
	err = json.Unmarshal(body, &tokens)
	if err != nil {
		log.Error().Msgf("Failed to parse tokens from response: %v", err)
		return nil, fmt.Errorf("failed to parse tokens: %w", err)
	}

	return &tokens, nil
}
