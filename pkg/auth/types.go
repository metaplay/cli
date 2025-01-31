/*
 * Copyright Metaplay. All rights reserved.
 */
package auth

// Type for Metaplay Auth. Get this using OAuth2 code exchange with
// auth.metaplay.dev.
type TokenSet struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`

	// Ignored: not useful, use expires_at from AccessToken instead
	// ExpiresIn    int    `json:"expires_in,omitempty"`
}

/**
 * OIDC UserInfo Response object.
 * @see https://openid.net/specs/openid-connect-core-1_0.html#UserInfo
 */
type UserInfoResponse struct {
	Subject    string   `json:"sub"`
	Email      string   `json:"email"`
	Picture    string   `json:"picture"`
	GivenName  string   `json:"given_name"`
	FamilyName string   `json:"family_name"`
	Name       string   `json:"name"`
	Roles      []string `json:"https://schemas.metaplay.io/roles"`
}
