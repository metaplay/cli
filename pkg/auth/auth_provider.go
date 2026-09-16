/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// Name of the built-in auth provider. Also its session ID, which is why a
// provider loaded from a file is not allowed to claim it.
const metaplayAuthProviderName = "Metaplay Auth"

// AuthProviderFileEnvVar names a YAML file describing the auth provider to use
// in place of the built-in one. This is how the CLI is pointed at a Metaplay
// platform other than the managed one.
const AuthProviderFileEnvVar = "METAPLAYCLI_AUTH_PROVIDER_FILE"

// OAuth2 scopes requested when a provider does not name its own.
const defaultAuthScopes = "openid profile email offline_access"

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

// NewDefaultAuthProvider resolves the auth provider used when a project does not
// name one of its own: Metaplay Auth, or the provider described by the file named
// in METAPLAYCLI_AUTH_PROVIDER_FILE when that is set, which is how a platform
// other than the managed one is reached.
func NewDefaultAuthProvider() (*AuthProviderConfig, error) {
	providerFilePath := os.Getenv(AuthProviderFileEnvVar)
	if providerFilePath == "" {
		return newMetaplayAuthProvider(), nil
	}

	log.Debug().Msgf("Loading auth provider from %s=%s", AuthProviderFileEnvVar, providerFilePath)
	return loadAuthProviderConfigFile(providerFilePath)
}

// The auth provider for Metaplay's managed platform.
func newMetaplayAuthProvider() *AuthProviderConfig {
	return &AuthProviderConfig{
		Name:             metaplayAuthProviderName,
		ClientID:         "c16ea663-ced3-46c6-8f85-38c9681fe1f0",
		AuthEndpoint:     "https://auth.metaplay.dev/oauth2/auth",
		TokenEndpoint:    "https://auth.metaplay.dev/oauth2/token",
		RevokeEndpoint:   "https://auth.metaplay.dev/oauth2/revoke",
		UserInfoEndpoint: "https://portal.metaplay.dev/api/external/userinfo",
		Scopes:           defaultAuthScopes,
		Audience:         "", // not used?
	}
}

// Load an auth provider from a YAML file on disk.
func loadAuthProviderConfigFile(filePath string) (*AuthProviderConfig, error) {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return nil, clierrors.Wrapf(err, "Failed to read auth provider file '%s'", filePath).
			WithSuggestion(fmt.Sprintf("Check that %s names a readable file", AuthProviderFileEnvVar))
	}

	provider, err := parseAuthProviderConfig(contents)
	if err != nil {
		return nil, clierrors.Wrapf(err, "Invalid auth provider file '%s'", filePath).
			WithSuggestion("Fix the file, or unset " + AuthProviderFileEnvVar + " to use Metaplay Auth")
	}

	return provider, nil
}

// Parse and validate an auth provider from the contents of a YAML file.
func parseAuthProviderConfig(contents []byte) (*AuthProviderConfig, error) {
	// Decode strictly: a misspelled field in a hand-written file would otherwise
	// be silently ignored and send the login somewhere unintended.
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)

	var provider AuthProviderConfig
	if err := decoder.Decode(&provider); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	if provider.Scopes == "" {
		provider.Scopes = defaultAuthScopes
	}

	if err := validateAuthProviderConfig(&provider); err != nil {
		return nil, err
	}

	return &provider, nil
}

// Validate that an auth provider names everything a login needs.
func validateAuthProviderConfig(provider *AuthProviderConfig) error {
	var problems []string

	switch provider.Name {
	case "":
		problems = append(problems, "required field 'name' is missing")
	case metaplayAuthProviderName:
		problems = append(problems, fmt.Sprintf("field 'name' must not be '%s', which is reserved for the built-in provider", metaplayAuthProviderName))
	}

	if provider.ClientID == "" {
		problems = append(problems, "required field 'clientId' is missing")
	}

	endpoints := []struct {
		fieldName string
		value     string
	}{
		{"authEndpoint", provider.AuthEndpoint},
		{"tokenEndpoint", provider.TokenEndpoint},
		{"revokeEndpoint", provider.RevokeEndpoint},
		{"userInfoEndpoint", provider.UserInfoEndpoint},
	}
	for _, endpoint := range endpoints {
		if endpoint.value == "" {
			problems = append(problems, fmt.Sprintf("required field '%s' is missing", endpoint.fieldName))
			continue
		}
		parsed, err := url.Parse(endpoint.value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			problems = append(problems, fmt.Sprintf("field '%s' ('%s') is not an http(s) URL", endpoint.fieldName, endpoint.value))
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	return nil
}
