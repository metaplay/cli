/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type authShowTokensOpts struct {
	UsePositionalArgs

	argAuthProvider string
}

func init() {
	o := authShowTokensOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argAuthProvider, "AUTH_PROVIDER", "Name of the auth provider to use. Defaults to 'metaplay'.")

	cmd := &cobra.Command{
		Use:   "show-tokens [AUTH_PROVIDER]",
		Short: "Print the active tokens as JSON to stdout",
		Long: renderLong(&o, `
			Print the currently active authentication tokens to stdout.

			The default auth provider is 'metaplay'. If you have multiple auth providers configured in your
			'metaplay-project.yaml', you can specify the name of the provider you want to use with the
			argument AUTH_PROVIDER.

			{Arguments}
		`),
		Run: runCommand(&o),
	}

	cmd.Hidden = true
	authCmd.AddCommand(cmd)
}

func (o *authShowTokensOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

// decodeJWT decodes a JWT token and returns the payload as a map
func decodeJWT(tokenString string) (map[string]any, error) {
	// Split the token into parts
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, nil
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	// Parse the JSON payload
	var claims map[string]any
	err = json.Unmarshal(payload, &claims)
	if err != nil {
		return nil, err
	}

	return claims, nil
}

func (o *authShowTokensOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve auth provider.
	authProvider, err := getAuthProvider(project, o.argAuthProvider)
	if err != nil {
		return err
	}

	// Load tokenSet from keyring & refresh if needed.
	tokenSet, err := auth.LoadAndRefreshTokenSet(authProvider)
	if err != nil {
		return err
	}

	// Handle missing tokens (not logged in).
	if tokenSet == nil {
		log.Warn().Msg("Not logged in! Sign in first with 'metaplay auth login' or 'metaplay auth machine-login'")
		os.Exit(1)
	}

	// Decode and log the access token at debug level
	if tokenSet.AccessToken != "" {
		accessTokenClaims, err := decodeJWT(tokenSet.AccessToken)
		if err != nil {
			log.Debug().Msgf("Failed to decode access token: %v", err)
		} else {
			accessTokenJSON, _ := json.MarshalIndent(accessTokenClaims, "", "  ")
			log.Debug().Msgf("Decoded access token: %s", string(accessTokenJSON))
		}
	}

	// Decode and log the ID token at debug level
	if tokenSet.IDToken != "" {
		// Use standard JWT decoding for ID token
		idTokenClaims, err := decodeJWT(tokenSet.IDToken)
		if err != nil {
			log.Debug().Msgf("Failed to decode ID token: %v", err)
		} else {
			// Log all claims without special handling
			idTokenJSON, _ := json.MarshalIndent(idTokenClaims, "", "  ")
			log.Debug().Msgf("Decoded ID token: %s", string(idTokenJSON))
		}
	}

	// Marshal tokenSet to JSON.
	bytes, err := json.MarshalIndent(tokenSet, "", "  ")
	if err != nil {
		log.Panic().Msgf("failed to serialize tokens into JSON: %v", err)
	}

	// Print as string.
	log.Info().Msg(string(bytes))
	return nil
}
