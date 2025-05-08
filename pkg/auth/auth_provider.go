/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import "github.com/metaplay/cli/pkg/common"

// OAuth2 client configuration.
type AuthProviderConfig struct {
	Name             string `yaml:"name"`             // Name of the provider (used as sessionID as well).
	ClientID         string `yaml:"clientId"`         // OAuth2 client ID.
	AuthEndpoint     string `yaml:"authEndpoint"`     // Eg, "https://portal.metaplay.dev/oauth2/auth".
	TokenEndpoint    string `yaml:"tokenEndpoint"`    // Eg, "https://portal.metaplay.dev/oauth2/token".
	UserInfoEndpoint string `yaml:"userInfoEndpoint"` // Eg, "https://portal.metaplay.dev/api/external/userinfo"
	Scopes           string `yaml:"scopes"`           // Eg, "openid profile email offline_access"
	Audience         string `yaml:"audience"`         // Eg, "managed-gameservers"
}

func (provider *AuthProviderConfig) GetSessionID() string {
	return provider.Name

	// // Concatenate all fields into a single string
	// data := strings.Join([]string{
	// 	provider.ClientID,
	// 	provider.AuthEndpoint,
	// 	provider.TokenEndpoint,
	// 	provider.UserInfoEndpoint,
	// 	provider.Scopes,
	// 	provider.Audience,
	// }, "|")

	// // Compute SHA-256 hash
	// h := sha256.New()
	// h.Write([]byte(data))
	// hashBytes := h.Sum(nil)

	// // Convert to hex string and return
	// return hex.EncodeToString(hashBytes)
}

// Create a default AuthProvider that uses Metaplay Auth.
func NewMetaplayAuthProvider() *AuthProviderConfig {
	portalBaseURL := common.PortalBaseURL

	// Special handling for Tilt setup portal.
	if portalBaseURL == "http://portal.metaplay-dev.localhost" {
		return &AuthProviderConfig{
			Name:             "Metaplay Auth (tilt)",
			ClientID:         "c16ea663-ced3-46c6-8f85-38c9681fe1f0",
			AuthEndpoint:     "http://auth.metaplay-dev.localhost/oauth2/auth",
			TokenEndpoint:    "http://auth.metaplay-dev.localhost/oauth2/token",
			UserInfoEndpoint: "http://portal.metaplay-dev.localhost/api/external/userinfo",
			Scopes:           "openid profile email offline_access",
			Audience:         "", // not used?
		}
	}

	// Production portal.
	return &AuthProviderConfig{
		Name:             "Metaplay Auth",
		ClientID:         "c16ea663-ced3-46c6-8f85-38c9681fe1f0",
		AuthEndpoint:     "https://auth.metaplay.dev/oauth2/auth",
		TokenEndpoint:    "https://auth.metaplay.dev/oauth2/token",
		UserInfoEndpoint: "https://portal.metaplay.dev/api/external/userinfo",
		Scopes:           "openid profile email offline_access",
		Audience:         "", // not used?
	}
}
