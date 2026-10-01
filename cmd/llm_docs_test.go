/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		//nolint:errorlint // Wants this exact error, not merely one in its chain.
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

	t.Run("FailedPrecondition on a directory suggests listing it", func(t *testing.T) {
		grpcErr := status.Error(codes.FailedPrecondition, "path is a directory: docs/cloud-deployments/")
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		wantCmd := `'metaplay llm-docs glob "*" --path "docs/cloud-deployments"'`
		if !strings.Contains(cliErr.Suggestion, wantCmd) {
			t.Errorf("suggestion missing %s: %q", wantCmd, cliErr.Suggestion)
		}
		// Most directories have no index.md, so it must not be the primary advice.
		if strings.Contains(cliErr.Suggestion, "read '") {
			t.Errorf("suggestion should not tell the caller to read an index.md: %q", cliErr.Suggestion)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "path is a directory: docs/cloud-deployments/" {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
	})

	t.Run("FailedPrecondition on a directory quotes a path with spaces", func(t *testing.T) {
		grpcErr := status.Error(codes.FailedPrecondition, "path is a directory: samples/My Game")
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Suggestion, `--path "samples/My Game"`) {
			t.Errorf("suggestion does not quote the path: %q", cliErr.Suggestion)
		}
	})

	t.Run("FailedPrecondition on a directory without a path stays generic", func(t *testing.T) {
		grpcErr := status.Error(codes.FailedPrecondition, "path is a directory")
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Suggestion, `--path "<path>"`) {
			t.Errorf("suggestion missing generic placeholder: %q", cliErr.Suggestion)
		}
	})

	t.Run("FailedPrecondition not about a directory has no suggestion", func(t *testing.T) {
		grpcErr := status.Error(codes.FailedPrecondition, "index not ready")
		got := wrapLLMDocsError(grpcErr, "search documentation")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if cliErr.Suggestion != "" {
			t.Errorf("expected no suggestion, got %q", cliErr.Suggestion)
		}
	})

	t.Run("OutOfRange explains the offset", func(t *testing.T) {
		msg := "offset is beyond end of file: offset 500 is beyond end of file (54 lines total)"
		grpcErr := status.Error(codes.OutOfRange, msg)
		got := wrapLLMDocsError(grpcErr, "read file")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "out of bounds") || !strings.Contains(cliErr.Message, "read file") {
			t.Errorf("unexpected message: %q", cliErr.Message)
		}
		if !strings.Contains(cliErr.Suggestion, "--offset") {
			t.Errorf("suggestion missing --offset: %q", cliErr.Suggestion)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != msg {
			t.Errorf("expected gRPC message in details, got %v", cliErr.Details)
		}
		if cliErr.Code != clierrors.ExitRuntime {
			t.Errorf("expected ExitRuntime, got %d", cliErr.Code)
		}
	})

	t.Run("Canceled wraps cause", func(t *testing.T) {
		grpcErr := status.Error(codes.Canceled, "ripgrep cancelled")
		got := wrapLLMDocsError(grpcErr, "run ripgrep")
		cliErr, ok := clierrors.AsCLIError(got)
		if !ok {
			t.Fatalf("expected *CLIError, got %T", got)
		}
		if !strings.Contains(cliErr.Message, "cancelled") || !strings.Contains(cliErr.Message, "run ripgrep") {
			t.Errorf("unexpected message: %q", cliErr.Message)
		}
		if cliErr.Cause == nil {
			t.Error("expected cause to be preserved")
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
		md, err := c.GetRequestMetadata(t.Context())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if md != nil {
			t.Errorf("expected nil metadata for empty token, got %v", md)
		}
	})

	t.Run("non-empty token sets bearer header", func(t *testing.T) {
		c := bearerCredentials{token: "abc123"}
		md, err := c.GetRequestMetadata(t.Context())
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

// newLLMDocsTestCmd returns a command carrying the flags that register
// defines, parsed from argv. Callers pass the real command's registerFlags,
// so these tests fail if a flag's name or type changes.
func newLLMDocsTestCmd(t *testing.T, register func(*pflag.FlagSet), argv []string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	register(cmd.Flags())
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("failed to parse flags %v: %v", argv, err)
	}
	return cmd
}

// checkLLMDocsPrepareErr asserts err is nil when wantErr is empty, and
// otherwise a usage error mentioning wantErr.
func checkLLMDocsPrepareErr(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !clierrors.IsUsageError(err) {
		t.Errorf("expected usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Errorf("error %q does not mention %q", err.Error(), wantErr)
	}
}

func TestLLMDocsReadOptsPrepare(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{"no flags", nil, ""},
		{"offset 1", []string{"--offset", "1"}, ""},
		{"offset max int32", []string{"--offset", "2147483647"}, ""},
		{"offset 0", []string{"--offset", "0"}, "--offset"},
		{"offset negative", []string{"--offset", "-1"}, "--offset"},
		{"offset above int32", []string{"--offset", "2147483648"}, "--offset"},
		{"limit 1", []string{"--limit", "1"}, ""},
		{"limit max int32", []string{"--limit", "2147483647"}, ""},
		{"limit 0", []string{"--limit", "0"}, "--limit"},
		{"limit wraps to 1 as int32", []string{"--limit", "4294967297"}, "--limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &llmDocsReadOpts{}
			cmd := newLLMDocsTestCmd(t, o.registerFlags, tc.argv)
			checkLLMDocsPrepareErr(t, o.Prepare(cmd, nil), tc.wantErr)
		})
	}
}

func TestLLMDocsRipgrepOptsPrepare(t *testing.T) {
	maxStr := strconv.Itoa(llmDocsMaxContextLines)
	overStr := strconv.Itoa(llmDocsMaxContextLines + 1)
	tests := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{"defaults", nil, ""},
		{"context at max", []string{"--context", maxStr}, ""},
		{"short before and after", []string{"-B", "3", "-A", "5"}, ""},
		{"short context negative", []string{"-C", "-1"}, "--context"},
		{"context above max", []string{"--context", overStr}, "--context"},
		{"context wraps as int32", []string{"-C", "4294967297"}, "--context"},
		{"before negative", []string{"--before-context", "-2"}, "--before-context"},
		{"after above max", []string{"-A", overStr}, "--after-context"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &llmDocsRipgrepOpts{}
			cmd := newLLMDocsTestCmd(t, o.registerFlags, tc.argv)
			checkLLMDocsPrepareErr(t, o.Prepare(cmd, nil), tc.wantErr)
		})
	}
}

func TestLLMDocsRipgrepGlobFlag(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"none", nil, nil},
		{
			"brace alternatives are not split at commas",
			[]string{"--glob", "*.{cs,md}", "--glob", "!**/Tests/**"},
			[]string{"*.{cs,md}", "!**/Tests/**"},
		},
		{"equals form keeps commas", []string{"--glob=a,b"}, []string{"a,b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &llmDocsRipgrepOpts{}
			newLLMDocsTestCmd(t, o.registerFlags, tc.argv)
			if !slices.Equal(o.flagGlobs, tc.want) {
				t.Errorf("globs = %q, want %q", o.flagGlobs, tc.want)
			}
		})
	}
}
