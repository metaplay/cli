/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/httputil"
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

// AccessTokenExpiresAt returns when the access token of the tokenSet expires,
// read from its own exp claim.
func AccessTokenExpiresAt(tokenSet *TokenSet) (time.Time, error) {
	return getAccessTokenExpiresAt(tokenSet)
}

// Load the current token set. If not logged in, just return empty tokens.
// If logged in and tokens have expired, refresh the tokens. If the refresh
// fails, return an error.
// \todo Forget the tokens if the refresh fails (due to keys already used)
func LoadAndRefreshTokenSet(authProvider *AuthProviderConfig) (*TokenSet, error) {
	return LoadAndRefreshTokenSetWithin(authProvider, 0)
}

// LoadAndRefreshTokenSetWithin is LoadAndRefreshTokenSet for a caller that
// hands the token on and tells its recipient the token expires margin earlier
// than it does, as a kubectl credential plugin does. It refreshes on that same
// boundary: a token expiring within margin is refreshed now, rather than
// handed on with less life left than its recipient was promised.
func LoadAndRefreshTokenSetWithin(authProvider *AuthProviderConfig, margin time.Duration) (*TokenSet, error) {
	// Hold the session lock from loading the session to saving its refresh. A
	// process that waited for it then loads the refreshed tokens, rather than
	// presenting the refresh token again, which revokes the session.
	unlock, err := lockSessionStore()
	if err != nil {
		return nil, clierrors.Wrap(err, "Failed to lock stored credentials").
			WithSuggestion("Try again, and check for a 'metaplay' process that has not exited")
	}
	defer unlock()

	// Get current session (including credentials).
	sessionState, err := loadSessionState(authProvider)
	if err != nil {
		// A provider mismatch already names the command that resolves it. Wrapping
		// buries that: displayError prints only the outermost suggestion, and a bare
		// 'metaplay auth login' signs in to the default provider rather than the one
		// asked for, so following the hint changes nothing.
		if errors.Is(err, ErrSessionProviderMismatch) {
			return nil, err
		}
		return nil, clierrors.Wrap(err, "Failed to load stored credentials").
			WithSuggestion("Run 'metaplay auth login' to re-authenticate")
	}

	// If no tokens, user is not logged in; return empty token set.
	if sessionState == nil {
		return nil, nil
	}

	// Resolve when access token expires.
	tokenSet := sessionState.TokenSet
	expiresAt, err := getAccessTokenExpiresAt(tokenSet)
	if err != nil {
		return nil, clierrors.Wrap(err, "Failed to parse access token expiration").
			WithSuggestion("Run 'metaplay auth login' to re-authenticate")
	}

	// Compare expiration time with the current time, brought forward by the
	// margin.
	isExpired := time.Now().Add(margin).After(expiresAt)

	// Refresh the tokenSet (if we have a refresh token -- machine users do not).
	if isExpired {
		if tokenSet.RefreshToken != "" {
			// Refresh the tokenSet.
			tokenSet, err = refreshTokenSet(tokenSet, authProvider)
			if err != nil {
				return nil, clierrors.Wrap(err, "Failed to refresh authentication tokens").
					WithSuggestion("Your session may have expired. Run 'metaplay auth login' to re-authenticate")
			}

			// Persist the refreshed tokens.
			err = saveSessionState(authProvider, sessionState.UserType, tokenSet)
			if err != nil {
				return nil, clierrors.Wrap(err, "Failed to persist refreshed tokens")
			}
		} else if margin > 0 && time.Now().Before(expiresAt) {
			return nil, clierrors.Newf("Access token expires within %v and cannot be refreshed", margin).
				WithSuggestion("Run 'metaplay auth machine-login' to obtain new credentials")
		} else {
			return nil, clierrors.New("Access token has expired and cannot be refreshed").
				WithSuggestion("Run 'metaplay auth machine-login' to obtain new credentials")
		}
	}

	return tokenSet, nil
}

// Refresh the tokenSet. Return a new tokenSet that was returned by the token endpoint.
// The caller holds the session lock.
func refreshTokenSet(tokenSet *TokenSet, authProvider *AuthProviderConfig) (*TokenSet, error) {
	// Create URL-encoded form data
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tokenSet.RefreshToken)
	data.Set("scope", authProvider.Scopes) //"openid offline_access")
	data.Set("client_id", authProvider.ClientID)

	// Make the HTTP request with retry logic for transient errors
	body, statusCode, err := httputil.PostFormWithRetry(authProvider.TokenEndpoint, data.Encode())
	if err != nil {
		log.Error().Msgf("Failed to refresh tokens via endpoint %s: %v", authProvider.TokenEndpoint, err)
		if err.Error() == "x509: certificate signed by unknown authority" {
			return nil, clierrors.Wrap(err, "SSL certificate validation failed during token refresh").
				WithSuggestion("Check your network connection — someone may be intercepting your traffic")
		}
		return nil, clierrors.Wrapf(err, "Failed to refresh tokens via %s", authProvider.TokenEndpoint)
	}

	// Check for a non-OK response (after retries exhausted for transient errors)
	if statusCode != http.StatusOK {
		log.Error().Msgf("Failed to refresh tokens. Response: %s", body)

		// Only a refused grant ends the session: RFC 6749 answers one with 400, or
		// 401 for the client. Anything else, such as a server error outlasting the
		// retries, says nothing about the session, so keep it for the next attempt.
		if statusCode != http.StatusBadRequest && statusCode != http.StatusUnauthorized {
			return nil, clierrors.Newf("Token endpoint %s answered the refresh with status %d", authProvider.TokenEndpoint, statusCode).
				WithSuggestion("Try again in a moment")
		}

		// Remove the session state (something has gone badly wrong).
		log.Debug().Msg("Clearing local credentials...")
		err = deleteSessionState(authProvider)
		if err != nil {
			return nil, clierrors.Wrap(err, "Failed to clean up expired credentials")
		}

		log.Debug().Msg("Local credentials removed.")
		return nil, clierrors.New("Session expired and could not be refreshed").
			WithSuggestion("Run 'metaplay auth login' to re-authenticate")
	}

	var tokens TokenSet
	err = json.Unmarshal(body, &tokens)
	if err != nil {
		log.Error().Msgf("Failed to parse tokens from response: %v", err)
		return nil, clierrors.Wrap(err, "Failed to parse authentication tokens")
	}

	return mergeRefreshedTokenSet(tokenSet, &tokens), nil
}

// mergeRefreshedTokenSet treats the refresh response as a delta: access_token comes from it,
// while id_token and refresh_token are carried forward when omitted (Ory drops id_token on
// refresh; refresh_token may be absent when rotation is disabled).
func mergeRefreshedTokenSet(previous, refreshed *TokenSet) *TokenSet {
	merged := *refreshed
	if merged.IDToken == "" {
		merged.IDToken = previous.IDToken
	}
	if merged.RefreshToken == "" {
		merged.RefreshToken = previous.RefreshToken
	}
	return &merged
}
