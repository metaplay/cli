/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package auth

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"
)

type MetaplayIDToken struct {
	*oidc.IDToken // Include all standard claims

	MetaplayEmail string   `json:"https://schemas.metaplay.io/email"` // Email of the Metaplay portal user.
	MetaplayRoles []string `json:"https://schemas.metaplay.io/roles"` // Roles in Metaplay environments.
}

func ResolveMetaplayIDToken(ctx context.Context, authProvider *AuthProviderConfig, idTokenStr string) (MetaplayIDToken, error) {
	authIssuer := "https://auth.metaplay.dev"

	// Create a new OpenID Connect provider
	provider, err := oidc.NewProvider(ctx, authIssuer)
	if err != nil {
		log.Fatal().Msgf("Failed to create OIDC provider: %v", err)
	}

	// Set up a verifier for the ID token
	verifier := provider.Verifier(&oidc.Config{ClientID: authProvider.ClientID})

	// Parse and verify the ID token
	idToken, err := verifier.Verify(ctx, idTokenStr)
	if err != nil {
		log.Fatal().Msgf("Failed to verify ID token: %v", err)
	}

	// Extract custom Metaplay claims
	type MetaplayClaims struct {
		Email string   `json:"https://schemas.metaplay.io/email"`
		Roles []string `json:"https://schemas.metaplay.io/roles"`
	}
	var claims MetaplayClaims
	if err := idToken.Claims(&claims); err != nil {
		log.Fatal().Msgf("Failed to parse ID token claims: %v", err)
	}

	// Convert to MetaplayIDToken
	return MetaplayIDToken{
		IDToken:       idToken,
		MetaplayEmail: claims.Email,
		MetaplayRoles: claims.Roles,
	}, nil
}
