/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
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

// AuthProviderFileEnvVar names a YAML file with an AuthProviderConfig to use in place of
// Metaplay Auth. Internal dev tool for targeting a non-managed platform, see DEVELOPMENT.md.
const AuthProviderFileEnvVar = "METAPLAYCLI_AUTH_PROVIDER_FILE"

// OAuth2 scopes requested when a provider does not name its own.
const defaultAuthScopes = "openid profile email offline_access"

// Sessions of a provider loaded from AuthProviderFileEnvVar are namespaced with this
// prefix. The name in such a file is free text chosen by whoever wrote it, and would
// otherwise be able to claim the session of a provider defined in metaplay-project.yaml.
//
// The namespace is one-way: only the file validator applies the prefix rule, so a
// project provider named 'file:Example' still collides with a file provider 'Example'.
const fileAuthProviderSessionPrefix = "file:"

// OAuth2 client configuration.
type AuthProviderConfig struct {
	Name             string `yaml:"name"`             // Name of the provider. The session ID derives from it; see GetSessionID.
	ClientID         string `yaml:"clientId"`         // OAuth2 client ID.
	AuthEndpoint     string `yaml:"authEndpoint"`     // Eg, "https://auth.metaplay.dev/oauth2/auth".
	TokenEndpoint    string `yaml:"tokenEndpoint"`    // Eg, "https://auth.metaplay.dev/oauth2/token".
	RevokeEndpoint   string `yaml:"revokeEndpoint"`   // Eg, "https://auth.metaplay.dev/oauth2/revoke".
	UserInfoEndpoint string `yaml:"userInfoEndpoint"` // Eg, "https://portal.metaplay.dev/api/external/userinfo"
	Scopes           string `yaml:"scopes"`           // Eg, "openid profile email offline_access"
	Audience         string `yaml:"audience"`         // Eg, "managed-gameservers"

	// Set when this provider came from AuthProviderFileEnvVar rather than from the
	// built-in definition or metaplay-project.yaml. Unexported, so YAML decoding of a
	// provider file cannot set it.
	loadedFromFile bool

	// Key in metaplay-project.yaml's authProviders map, stamped on load; empty for the
	// built-in provider and for one loaded from a file. Resolution matches this key
	// before Name, so it is what identifies a provider. Unexported like loadedFromFile,
	// with a setter because the project is parsed in another package.
	projectKey string
}

// SetProjectKey records the authProviders key this provider is filed under. For the
// project loader; nothing else has reason to call it.
func (provider *AuthProviderConfig) SetProjectKey(key string) {
	provider.projectKey = key
}

func (provider *AuthProviderConfig) GetSessionID() string {
	if provider.loadedFromFile {
		return fileAuthProviderSessionPrefix + provider.Name
	}
	return provider.Name
}

// LoginCommand returns the command that signs in to this provider, for error
// suggestions. A bare 'metaplay auth login' resolves the default provider, so it
// reaches the built-in provider and a file one, but never a project-defined provider.
//
// It names the authProviders key, not Name. Name is not a reliable argument: two
// providers may share one, a Name may collide with another provider's key, and a
// provider named 'metaplay' would resolve back to the default.
//
// It branches on provenance rather than IsBuiltinMetaplayAuth, which a project provider
// can make true by taking the built-in name — handing the bare command to exactly the
// providers it cannot reach.
func (provider *AuthProviderConfig) LoginCommand() string {
	if provider.projectKey != "" {
		return fmt.Sprintf("metaplay auth login %s", provider.projectKey)
	}
	return "metaplay auth login"
}

// Fingerprint identifies the platform and client a session's tokens were minted for.
// The token endpoint issues them and the client ID is who they were issued to, so the
// pair pins the audience. Stored with the session, to catch a provider that shares a
// session key before it is handed tokens it did not mint.
//
// The guarantee is narrow. Only these two fields are hashed, so a provider reusing both
// is accepted, and revokeEndpoint and userInfoEndpoint are not pinned at all. Sessions
// written before this field existed carry no fingerprint and any provider sharing their
// key is accepted — see sessionBelongsToProvider. This guards against collision and
// misconfiguration among providers that are all trusted. It is not a boundary against
// a project config that is not.
//
// The endpoint is hashed verbatim, so a host differing only in letter case or an
// explicit ':443' reads as a different provider.
func (provider *AuthProviderConfig) Fingerprint() string {
	sum := sha256.Sum256([]byte(provider.ClientID + "\n" + provider.TokenEndpoint))
	return hex.EncodeToString(sum[:8])
}

// IsBuiltinMetaplayAuth reports whether this is the built-in Metaplay Auth provider,
// as opposed to one loaded from AuthProviderFileEnvVar or named by the project. A
// session from any other provider belongs to a different platform: its tokens name
// that platform as their audience and must not be presented to Metaplay-operated
// services.
//
// A file provider cannot claim this name; validation rejects it. A provider defined in
// metaplay-project.yaml can, because pkg/metaproj validates it separately and does not
// apply the reserved name. Such a provider reports true here and shares the built-in
// session key, so a caller gating a token on this is trusting the project file.
func (provider *AuthProviderConfig) IsBuiltinMetaplayAuth() bool {
	return !provider.loadedFromFile && provider.Name == metaplayAuthProviderName
}

// NewDefaultAuthProvider resolves the auth provider used when a project does not
// name one of its own: Metaplay Auth, or the provider described by the file named
// in METAPLAYCLI_AUTH_PROVIDER_FILE when that is set, which is how a platform
// other than the managed one is reached.
func NewDefaultAuthProvider() (*AuthProviderConfig, error) {
	providerFilePath := os.Getenv(AuthProviderFileEnvVar)
	if providerFilePath == "" {
		return NewMetaplayAuthProvider(), nil
	}

	log.Debug().Msgf("Loading auth provider from %s=%s", AuthProviderFileEnvVar, providerFilePath)
	return loadAuthProviderConfigFile(providerFilePath)
}

// NewMetaplayAuthProvider returns the auth provider for Metaplay's managed platform.
// Commands should call NewDefaultAuthProvider instead, which honors AuthProviderFileEnvVar.
func NewMetaplayAuthProvider() *AuthProviderConfig {
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

	provider.loadedFromFile = true
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
			continue
		}
		// Cleartext is only ever acceptable when the traffic does not leave the
		// machine. Everything these endpoints carry is a secret: the authorization
		// code exchange, every refresh, the revoke call, and the bearer token sent
		// to userInfoEndpoint.
		if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
			problems = append(problems, fmt.Sprintf("field '%s' ('%s') must use https; plain http is allowed only for loopback hosts (localhost, *.localhost, 127.0.0.1, ::1)", endpoint.fieldName, endpoint.value))
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	return nil
}

// isLoopbackHost reports whether a URL host is meant to name the local machine, which
// is what local platform setups use (e.g. 'auth.example.localhost').
//
// Meant to, not proven to. RFC 6761 says resolvers SHOULD send the whole '.localhost'
// TLD to loopback and they disagree about whether they do. Go's does not: under
// CGO_ENABLED=0, which is how releases are built, a name below the apex goes to files
// then DNS like any other. Only the bare names and literal addresses are proof.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
