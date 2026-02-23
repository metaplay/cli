/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import "github.com/metaplay/cli/pkg/common"

// OAuth2 client configuration.
type AuthProviderConfig struct {
	Name             string `yaml:"name"`             // Name of the provider (used as sessionID as well).
	ClientID         string `yaml:"clientId"`         // OAuth2 client ID.
	AuthEndpoint     string `yaml:"authEndpoint"`     // Eg, "https://auth.metaplay.dev/oauth2/auth".
	TokenEndpoint    string `yaml:"tokenEndpoint"`    // Eg, "https://auth.metaplay.dev/oauth2/token".
	RevokeEndpoint   string `yaml:"revokeEndpoint"`   // Eg, "https://auth.metaplay.dev/oauth2/revoke".
	UserInfoEndpoint string `yaml:"userInfoEndpoint"` // Eg, "https://portal.metaplay.dev/api/external/userinfo"
	Scopes           string `yaml:"scopes"`           // Eg, "openid profile email offline_access"
	Audience         string `yaml:"audience"`         // Eg, "managed-gameservers"
}

func (provider *AuthProviderConfig) GetSessionID() string {
	return provider.Name
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
			RevokeEndpoint:   "http://auth.metaplay-dev.localhost/oauth2/revoke",
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
		RevokeEndpoint:   "https://auth.metaplay.dev/oauth2/revoke",
		UserInfoEndpoint: "https://portal.metaplay.dev/api/external/userinfo",
		Scopes:           "openid profile email offline_access",
		Audience:         "", // not used?
	}
}
