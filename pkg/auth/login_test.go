/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// makeToken builds a signed JWT from the given claims. The signature is irrelevant (claims are read
// without verification) but keeps the token well-formed.
func makeToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-key"))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return signed
}

func TestResolveUserInfo(t *testing.T) {
	t.Run("human profile from ID token", func(t *testing.T) {
		ts := &TokenSet{
			AccessToken: makeToken(t, jwt.MapClaims{"sub": "user-uuid"}),
			IDToken: makeToken(t, jwt.MapClaims{
				"sub":                               "user-uuid",
				"email":                             "petri.kero@metaplay.io",
				"given_name":                        "Petri",
				"family_name":                       "Kero",
				"picture":                           "https://example.com/a.png",
				"https://schemas.metaplay.io/roles": []any{"metaplay.idler.develop.game-admin"},
			}),
		}
		info, err := ResolveUserInfo(ts)
		if err != nil {
			t.Fatal(err)
		}
		if info.Subject != "user-uuid" {
			t.Errorf("Subject = %q", info.Subject)
		}
		if info.Email != "petri.kero@metaplay.io" {
			t.Errorf("Email = %q", info.Email)
		}
		if info.Name != "Petri Kero" {
			t.Errorf("Name = %q", info.Name)
		}
		if info.Picture != "https://example.com/a.png" {
			t.Errorf("Picture = %q", info.Picture)
		}
		if len(info.Roles) != 1 || info.Roles[0] != "metaplay.idler.develop.game-admin" {
			t.Errorf("Roles = %v", info.Roles)
		}
	})

	t.Run("machine: subject and namespaced email from access token", func(t *testing.T) {
		ts := &TokenSet{
			AccessToken: makeToken(t, jwt.MapClaims{
				"sub":                               "machine-uuid",
				"https://schemas.metaplay.io/email": "svc@metaplay.io",
			}),
		}
		info, err := ResolveUserInfo(ts)
		if err != nil {
			t.Fatal(err)
		}
		if info.Subject != "machine-uuid" {
			t.Errorf("Subject = %q", info.Subject)
		}
		if info.Email != "svc@metaplay.io" {
			t.Errorf("Email = %q (want namespaced fallback)", info.Email)
		}
		if info.Name != "" {
			t.Errorf("Name = %q, want empty for a machine user", info.Name)
		}
	})

	t.Run("ID token overrides access token email and adds name", func(t *testing.T) {
		ts := &TokenSet{
			AccessToken: makeToken(t, jwt.MapClaims{"sub": "u", "https://schemas.metaplay.io/email": "namespaced@x"}),
			IDToken:     makeToken(t, jwt.MapClaims{"sub": "u", "email": "standard@x", "given_name": "Ada", "family_name": "Lovelace"}),
		}
		info, err := ResolveUserInfo(ts)
		if err != nil {
			t.Fatal(err)
		}
		if info.Email != "standard@x" {
			t.Errorf("Email = %q, want standard@x (standard email wins)", info.Email)
		}
		if info.Name != "Ada Lovelace" {
			t.Errorf("Name = %q, want composed from given/family", info.Name)
		}
	})

	t.Run("error when tokens carry no subject", func(t *testing.T) {
		ts := &TokenSet{AccessToken: makeToken(t, jwt.MapClaims{"foo": "bar"})}
		if _, err := ResolveUserInfo(ts); err == nil {
			t.Error("expected an error when no subject is present")
		}
	})
}
