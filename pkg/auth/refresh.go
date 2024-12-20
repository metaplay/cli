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

// Extend the current tokenSet if expired. Otherwise, just return the current ones.
func EnsureValidTokenSet() (*TokenSet, error) {
	// Get current credentials.
	tokenSet, err := LoadTokenSet()
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}

	// Check for missing tokens (ie, not logged in).
	if tokenSet == nil {
		return nil, errors.New("not logged in")
	}

	// Resolve when access token expires.
	expiresAt, err := getAccessTokenExpiresAt(tokenSet)

	// Compare expiration time with the current time
	isExpired := time.Now().After(expiresAt)

	// Refresh the tokenSet (if we have a refresh token -- machine users do not).
	if isExpired {
		if tokenSet.RefreshToken != "" {
			// Refresh the tokenSet.
			tokenSet, err = refreshTokenSet(tokenSet)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh tokens: %w", err)
			}

			// Persist the refreshed tokens.
			err = SaveTokenSet(tokenSet)
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
func refreshTokenSet(tokenSet *TokenSet) (*TokenSet, error) {
	// Create URL-encoded form data
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tokenSet.RefreshToken)
	data.Set("scope", "openid offline_access")
	data.Set("client_id", clientID)

	// Prepare the POST request
	req, err := http.NewRequest("POST", tokenEndpoint, bytes.NewBufferString(data.Encode()))
	if err != nil {
		log.Error().Msgf("Failed to create HTTP request: %v", err)
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Error().Msgf("Failed to refresh tokens via endpoint %s: %v", tokenEndpoint, err)
		if err.Error() == "x509: certificate signed by unknown authority" {
			return nil, errors.New("failed to refresh tokens: SSL certificate validation failed. Is someone tampering with your internet connection?")
		}
		return nil, fmt.Errorf("failed to refresh tokens via %s: %w", tokenEndpoint, err)
	}
	defer resp.Body.Close()

	// Check for a non-OK response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error().Msgf("Failed to refresh tokens. Response: %s", body)
		log.Debug().Msg("Clearing local credentials...")

		// Remove the tokens (something has gone badly wrong).
		err = DeleteTokenSet()
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
