/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsLoopbackTarget(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{"plain localhost", "localhost", true},
		{"localhost with port", "localhost:50051", true},
		{"uppercase LOCALHOST", "LOCALHOST:443", true},
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 loopback with port", "127.0.0.1:50051", true},
		{"ipv4 loopback non-standard", "127.0.0.7:1234", true},
		{"ipv6 loopback bracketed with port", "[::1]:50051", true},
		{"ipv6 loopback bare", "::1", true},
		{"public hostname", "llm-docs.platform.metaplay.dev:443", false},
		{"public ipv4", "8.8.8.8:443", false},
		{"empty string", "", false},
		{"malformed host:port:port", "host:1:2", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopbackTarget(tc.target); got != tc.want {
				t.Errorf("isLoopbackTarget(%q) = %v, want %v", tc.target, got, tc.want)
			}
		})
	}
}

// signTestJWT returns a JWT signed with a throwaway key. userIdentityFromTokens
// uses ParseUnverified so any signature is accepted.
func signTestJWT(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-key"))
	if err != nil {
		t.Fatalf("failed to sign test JWT: %v", err)
	}
	return tok
}

func TestUserIdentityFromTokens(t *testing.T) {
	idTokWithBoth := signTestJWT(t, jwt.MapClaims{"sub": "id-sub", "email": "id@example.com"})
	accessTokWithBoth := signTestJWT(t, jwt.MapClaims{"sub": "access-sub", "email": "access@example.com"})
	idTokSubOnly := signTestJWT(t, jwt.MapClaims{"sub": "id-sub"})
	accessTokEmailOnly := signTestJWT(t, jwt.MapClaims{"email": "access@example.com"})
	tokNonStringClaims := signTestJWT(t, jwt.MapClaims{"sub": 123, "email": false})

	tests := []struct {
		name      string
		tokens    auth.TokenSet
		wantSub   string
		wantEmail string
	}{
		{
			name:      "id token has both",
			tokens:    auth.TokenSet{IDToken: idTokWithBoth, AccessToken: accessTokWithBoth},
			wantSub:   "id-sub",
			wantEmail: "id@example.com",
		},
		{
			name:      "access token only",
			tokens:    auth.TokenSet{AccessToken: accessTokWithBoth},
			wantSub:   "access-sub",
			wantEmail: "access@example.com",
		},
		{
			name:      "id has sub, access has email — fields filled from both",
			tokens:    auth.TokenSet{IDToken: idTokSubOnly, AccessToken: accessTokEmailOnly},
			wantSub:   "id-sub",
			wantEmail: "access@example.com",
		},
		{
			name:      "both empty",
			tokens:    auth.TokenSet{},
			wantSub:   "",
			wantEmail: "",
		},
		{
			name:      "malformed id token, valid access token",
			tokens:    auth.TokenSet{IDToken: "not-a-jwt", AccessToken: accessTokWithBoth},
			wantSub:   "access-sub",
			wantEmail: "access@example.com",
		},
		{
			name:      "non-string claims are ignored",
			tokens:    auth.TokenSet{IDToken: tokNonStringClaims},
			wantSub:   "",
			wantEmail: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := tc.tokens
			sub, email := userIdentityFromTokens(&ts)
			if sub != tc.wantSub {
				t.Errorf("sub = %q, want %q", sub, tc.wantSub)
			}
			if email != tc.wantEmail {
				t.Errorf("email = %q, want %q", email, tc.wantEmail)
			}
		})
	}
}

func TestWrapLLMDocsError(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if got := wrapLLMDocsError(nil, "read file"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-status error wraps generically", func(t *testing.T) {
		cause := errors.New("boom")
		got := wrapLLMDocsError(cause, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "read file") {
			t.Errorf("message missing action: %q", cliErr.Message)
		}
		if cliErr.Cause != cause {
			t.Errorf("cause not preserved, got %v", cliErr.Cause)
		}
		if cliErr.Code != clierrors.ExitRuntime {
			t.Errorf("expected ExitRuntime, got %d", cliErr.Code)
		}
	})

	t.Run("InvalidArgument", func(t *testing.T) {
		grpcErr := status.Error(codes.InvalidArgument, "bad pattern")
		got := wrapLLMDocsError(grpcErr, "run ripgrep")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "Invalid llm-docs request") ||
			!strings.Contains(cliErr.Message, "run ripgrep") {
			t.Errorf("unexpected message: %q", cliErr.Message)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "bad pattern" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("Unauthenticated suggests auth login", func(t *testing.T) {
		grpcErr := status.Error(codes.Unauthenticated, "token expired")
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Suggestion, "metaplay auth login") {
			t.Errorf("suggestion missing 'metaplay auth login': %q", cliErr.Suggestion)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "token expired" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("PermissionDenied suggests contacting admin", func(t *testing.T) {
		grpcErr := status.Error(codes.PermissionDenied, "not in allowlist")
		got := wrapLLMDocsError(grpcErr, "search documentation")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "not permitted") ||
			!strings.Contains(cliErr.Message, "search documentation") {
			t.Errorf("unexpected message: %q", cliErr.Message)
		}
		if cliErr.Suggestion == "" {
			t.Error("expected a suggestion")
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "not in allowlist" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("DeadlineExceeded suggests retry", func(t *testing.T) {
		grpcErr := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
		got := wrapLLMDocsError(grpcErr, "find files")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if cliErr.Cause == nil {
			t.Error("expected cause to be preserved")
		}
		if !strings.Contains(cliErr.Message, "timed out") {
			t.Errorf("message missing 'timed out': %q", cliErr.Message)
		}
		if cliErr.Suggestion == "" {
			t.Error("expected a suggestion")
		}
	})

	t.Run("NotFound carries suggestion and details", func(t *testing.T) {
		grpcErr := status.Error(codes.NotFound, "no such file")
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if cliErr.Suggestion == "" {
			t.Error("expected a suggestion")
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "no such file" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("FailedPrecondition carries details", func(t *testing.T) {
		grpcErr := status.Error(codes.FailedPrecondition, "index not ready")
		got := wrapLLMDocsError(grpcErr, "search documentation")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "search documentation") {
			t.Errorf("message missing action: %q", cliErr.Message)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "index not ready" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("Unavailable wraps cause and suggests override", func(t *testing.T) {
		grpcErr := status.Error(codes.Unavailable, "connection refused")
		got := wrapLLMDocsError(grpcErr, "read deployment info")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if cliErr.Cause == nil {
			t.Error("expected cause to be preserved")
		}
		if !strings.Contains(cliErr.Suggestion, "METAPLAYCLI_LLM_DOCS_ADDR") {
			t.Errorf("suggestion missing override env var: %q", cliErr.Suggestion)
		}
	})

	t.Run("default gRPC code falls through to generic wrap", func(t *testing.T) {
		grpcErr := status.Error(codes.Internal, "server exploded")
		got := wrapLLMDocsError(grpcErr, "find files")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "find files") {
			t.Errorf("message missing action: %q", cliErr.Message)
		}
		if cliErr.Cause == nil {
			t.Error("expected cause to be preserved on default branch")
		}
	})
}

func TestBearerCredentials(t *testing.T) {
	t.Run("empty token returns no metadata", func(t *testing.T) {
		c := bearerCredentials{}
		md, err := c.GetRequestMetadata(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if md != nil {
			t.Errorf("expected nil metadata for empty token, got %v", md)
		}
	})

	t.Run("non-empty token sets bearer header", func(t *testing.T) {
		c := bearerCredentials{token: "abc123"}
		md, err := c.GetRequestMetadata(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := md["authorization"]; got != "Bearer abc123" {
			t.Errorf("authorization = %q, want %q", got, "Bearer abc123")
		}
	})

	t.Run("RequireTransportSecurity mirrors requireTLS", func(t *testing.T) {
		if !(bearerCredentials{requireTLS: true}).RequireTransportSecurity() {
			t.Error("expected true when requireTLS=true")
		}
		if (bearerCredentials{requireTLS: false}).RequireTransportSecurity() {
			t.Error("expected false when requireTLS=false")
		}
	})
}
