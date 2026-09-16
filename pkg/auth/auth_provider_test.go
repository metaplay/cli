/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/common"
)

const validProviderYAML = `
name: Example Platform
clientId: 11111111-2222-3333-4444-555555555555
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
scopes: openid profile email offline_access
`

// Writing a provider file and pointing the environment variable at it is the
// documented way to reach another platform, so the tests go through it.
func writeProviderFile(t *testing.T, contents string) string {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "auth-provider.yaml")
	if err := os.WriteFile(filePath, []byte(contents), 0600); err != nil {
		t.Fatalf("failed to write provider file: %v", err)
	}
	return filePath
}

func TestNewDefaultAuthProvider_DefaultsToMetaplayAuth(t *testing.T) {
	t.Setenv(AuthProviderFileEnvVar, "")

	provider, err := NewDefaultAuthProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.Name != metaplayAuthProviderName {
		t.Errorf("Name = %q, want %q", provider.Name, metaplayAuthProviderName)
	}
	if provider.AuthEndpoint != "https://auth.metaplay.dev/oauth2/auth" {
		t.Errorf("AuthEndpoint = %q, want the Metaplay Auth endpoint", provider.AuthEndpoint)
	}
	if provider.UserInfoEndpoint != "https://portal.metaplay.dev/api/external/userinfo" {
		t.Errorf("UserInfoEndpoint = %q, want the Metaplay portal endpoint", provider.UserInfoEndpoint)
	}
}

// The provider used to switch on the portal base URL, which is how one specific
// local portal was reachable and no other was. No portal domain decides it now.
func TestNewDefaultAuthProvider_IgnoresPortalBaseURL(t *testing.T) {
	t.Setenv(AuthProviderFileEnvVar, "")

	originalPortalBaseURL := common.PortalBaseURL
	t.Cleanup(func() { common.PortalBaseURL = originalPortalBaseURL })

	for _, portalBaseURL := range []string{
		"http://portal.localhost",
		"https://portal.example.com",
		"https://portal.example.org",
	} {
		common.PortalBaseURL = portalBaseURL

		provider, err := NewDefaultAuthProvider()
		if err != nil {
			t.Fatalf("unexpected error for portal %q: %v", portalBaseURL, err)
		}
		if provider.Name != metaplayAuthProviderName {
			t.Errorf("portal %q: Name = %q, want %q", portalBaseURL, provider.Name, metaplayAuthProviderName)
		}
		if provider.AuthEndpoint != "https://auth.metaplay.dev/oauth2/auth" {
			t.Errorf("portal %q: AuthEndpoint = %q, want the Metaplay Auth endpoint", portalBaseURL, provider.AuthEndpoint)
		}
	}
}

func TestNewDefaultAuthProvider_LoadsProviderFile(t *testing.T) {
	t.Setenv(AuthProviderFileEnvVar, writeProviderFile(t, validProviderYAML))

	provider, err := NewDefaultAuthProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.Name != "Example Platform" {
		t.Errorf("Name = %q, want the name from the file", provider.Name)
	}
	if provider.ClientID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("ClientID = %q, want the client ID from the file", provider.ClientID)
	}
	if provider.TokenEndpoint != "https://auth.example.com/oauth2/token" {
		t.Errorf("TokenEndpoint = %q, want the endpoint from the file", provider.TokenEndpoint)
	}
	if provider.RevokeEndpoint != "https://auth.example.com/oauth2/revoke" {
		t.Errorf("RevokeEndpoint = %q, want the endpoint from the file", provider.RevokeEndpoint)
	}
	if provider.UserInfoEndpoint != "https://portal.example.com/api/external/userinfo" {
		t.Errorf("UserInfoEndpoint = %q, want the endpoint from the file", provider.UserInfoEndpoint)
	}
}

// The provider name is the session key, so another platform's session is stored
// beside the Metaplay Auth one rather than on top of it.
func TestNewDefaultAuthProvider_SessionIDDiffersFromMetaplayAuth(t *testing.T) {
	t.Setenv(AuthProviderFileEnvVar, writeProviderFile(t, validProviderYAML))

	provider, err := NewDefaultAuthProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.GetSessionID() == metaplayAuthProviderName {
		t.Errorf("GetSessionID() = %q, want a session separate from Metaplay Auth", provider.GetSessionID())
	}
}

func TestNewDefaultAuthProvider_MissingProviderFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	t.Setenv(AuthProviderFileEnvVar, missingPath)

	_, err := NewDefaultAuthProvider()
	if err == nil {
		t.Fatal("expected an error for a missing provider file")
	}
	if !strings.Contains(err.Error(), missingPath) {
		t.Errorf("error %q does not name the missing file", err.Error())
	}
}

func TestParseAuthProviderConfig(t *testing.T) {
	tests := []struct {
		name        string
		yamlData    string
		wantErrPart string // Substring the error must name; empty means success is expected.
		check       func(t *testing.T, provider *AuthProviderConfig)
	}{
		{
			name:     "complete config",
			yamlData: validProviderYAML,
			check: func(t *testing.T, provider *AuthProviderConfig) {
				if provider.Scopes != "openid profile email offline_access" {
					t.Errorf("Scopes = %q, want the scopes from the file", provider.Scopes)
				}
			},
		},
		{
			name: "scopes default when omitted",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			check: func(t *testing.T, provider *AuthProviderConfig) {
				if provider.Scopes != defaultAuthScopes {
					t.Errorf("Scopes = %q, want the default %q", provider.Scopes, defaultAuthScopes)
				}
			},
		},
		{
			name: "missing name",
			yamlData: `
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "name",
		},
		{
			name: "missing client id",
			yamlData: `
name: Self-hosted
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "clientId",
		},
		{
			name: "missing token endpoint",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "tokenEndpoint",
		},
		{
			name: "endpoint without a scheme",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "authEndpoint",
		},
		{
			name: "name reserved for Metaplay Auth",
			yamlData: `
name: Metaplay Auth
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "Metaplay Auth",
		},
		{
			name: "unknown field",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
tokenEndpint: https://auth.example.com/oauth2/token
`,
			wantErrPart: "tokenEndpint",
		},
		{
			name: "plain http to a remote host",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: https://auth.example.com/oauth2/auth
tokenEndpoint: http://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "tokenEndpoint",
		},
		{
			name: "plain http to a remote host names https",
			yamlData: `
name: Self-hosted
clientId: client-id
authEndpoint: http://auth.example.com/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "must use https",
		},
		{
			name: "plain http to localhost is allowed",
			yamlData: `
name: Local
clientId: client-id
authEndpoint: http://localhost:4444/oauth2/auth
tokenEndpoint: http://127.0.0.1:4444/oauth2/token
revokeEndpoint: http://[::1]:4444/oauth2/revoke
userInfoEndpoint: http://LOCALHOST:3000/api/external/userinfo
`,
			check: func(t *testing.T, provider *AuthProviderConfig) {
				if provider.Name != "Local" {
					t.Errorf("Name = %q, want %q", provider.Name, "Local")
				}
			},
		},
		{
			name: "plain http to a .localhost subdomain is allowed",
			yamlData: `
name: Tilt
clientId: client-id
authEndpoint: http://auth.metaplay-dev.localhost/oauth2/auth
tokenEndpoint: http://auth.metaplay-dev.localhost/oauth2/token
revokeEndpoint: http://auth.metaplay-dev.localhost/oauth2/revoke
userInfoEndpoint: http://portal.metaplay-dev.localhost/api/external/userinfo
`,
			check: func(t *testing.T, provider *AuthProviderConfig) {
				if provider.Name != "Tilt" {
					t.Errorf("Name = %q, want %q", provider.Name, "Tilt")
				}
			},
		},
		{
			name: "host ending in localhost without a dot is not loopback",
			yamlData: `
name: Sneaky
clientId: client-id
authEndpoint: http://notlocalhost/oauth2/auth
tokenEndpoint: https://auth.example.com/oauth2/token
revokeEndpoint: https://auth.example.com/oauth2/revoke
userInfoEndpoint: https://portal.example.com/api/external/userinfo
`,
			wantErrPart: "must use https",
		},
		{
			name:        "malformed yaml",
			yamlData:    "name: [unterminated\n",
			wantErrPart: "YAML",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := parseAuthProviderConfig([]byte(tc.yamlData))

			if tc.wantErrPart != "" {
				if err == nil {
					t.Fatalf("expected an error naming %q, got none", tc.wantErrPart)
				}
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Errorf("error %q does not name %q", err.Error(), tc.wantErrPart)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, provider)
			}
		})
	}
}
