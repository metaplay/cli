/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	clierrors "github.com/metaplay/cli/internal/errors"
)

func TestFetchUserInfo_ReturnsUserInfoError(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "unauthorized with explanation",
			status:      http.StatusUnauthorized,
			body:        `{"error":true,"statusCode":401,"statusMessage":"User not found with Metaplay Auth ID"}`,
			wantMessage: "User not found with Metaplay Auth ID",
		},
		{
			name:        "forbidden with empty body",
			status:      http.StatusForbidden,
			body:        "",
			wantMessage: "",
		},
		{
			name:        "not found",
			status:      http.StatusNotFound,
			body:        "no such endpoint",
			wantMessage: "no such endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := &AuthProviderConfig{UserInfoEndpoint: server.URL}
			_, err := FetchUserInfo(provider, &TokenSet{AccessToken: "token"})

			userInfoErr, ok := errors.AsType[*UserInfoError](err)
			if !ok {
				t.Fatalf("error %v is not a *UserInfoError", err)
			}
			if userInfoErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", userInfoErr.StatusCode, tt.status)
			}
			if userInfoErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", userInfoErr.Message, tt.wantMessage)
			}
			if !strings.Contains(err.Error(), http.StatusText(tt.status)) {
				t.Errorf("error %q does not contain the status", err.Error())
			}
		})
	}
}

func TestParseUserInfoErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", " \n\t ", ""},
		{"statusMessage field", `{"statusMessage":"User not found"}`, "User not found"},
		{"portal error shape with non-string fields", `{"error":true,"url":"/api/external/userinfo","statusCode":401,"statusMessage":"User not found with Metaplay Auth ID","message":"User not found with Metaplay Auth ID"}`, "User not found with Metaplay Auth ID"},
		{"non-string error field is skipped", `{"error":true}`, `{"error":true}`},
		{"message field", `{"message":"Token expired"}`, "Token expired"},
		{"oauth error_description wins over error", `{"error":"invalid_token","error_description":"Token revoked"}`, "Token revoked"},
		{"oauth error only", `{"error":"invalid_token"}`, "invalid_token"},
		{"json without known fields is kept raw", `{"code":42}`, `{"code":42}`},
		{"plain text is trimmed", "  Access denied\n", "Access denied"},
		{"html is collapsed to one line", "<html>\n  <body>Bad Gateway</body>\n</html>", "<html> <body>Bad Gateway</body> </html>"},
		{"invalid utf-8 is dropped", "bad\xffbyte", "badbyte"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUserInfoErrorMessage([]byte(tt.body))
			if got != tt.want {
				t.Errorf("parseUserInfoErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestParseUserInfoErrorMessage_Truncation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"ascii", strings.Repeat("x", 2000)},
		// 'ä' is two bytes, so a byte-based cut at 500 would split a character.
		{"multi-byte", "a" + strings.Repeat("ä", 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUserInfoErrorMessage([]byte(tt.body))
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
			if !strings.HasSuffix(got, "...") {
				t.Errorf("result does not end with an ellipsis: %q", got)
			}
			if n := utf8.RuneCountInString(strings.TrimSuffix(got, "...")); n != maxUserInfoErrorMessageLen {
				t.Errorf("result keeps %d characters, want %d", n, maxUserInfoErrorMessageLen)
			}
		})
	}
}

func TestConfirmSignIn(t *testing.T) {
	t.Run("success returns the user", func(t *testing.T) {
		var gotAuthorization string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuthorization = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sub":"abc","name":"Ada Lovelace","email":"ada@example.com"}`))
		}))
		defer server.Close()

		provider := &AuthProviderConfig{UserInfoEndpoint: server.URL}
		userinfo, err := confirmSignIn(provider, &TokenSet{AccessToken: "token"})
		if err != nil {
			t.Fatalf("confirmSignIn() error = %v", err)
		}
		if gotAuthorization != "Bearer token" {
			t.Errorf("Authorization = %q, want %q", gotAuthorization, "Bearer token")
		}
		if userinfo == nil || userinfo.Name != "Ada Lovelace" || userinfo.Email != "ada@example.com" {
			t.Errorf("userinfo = %+v", userinfo)
		}
	})

	t.Run("provider without a userinfo endpoint skips the call", func(t *testing.T) {
		userinfo, err := confirmSignIn(&AuthProviderConfig{}, &TokenSet{AccessToken: "token"})
		if err != nil {
			t.Fatalf("confirmSignIn() error = %v", err)
		}
		if userinfo != nil {
			t.Errorf("userinfo = %+v, want nil", userinfo)
		}
	})

	// 5xx answers are retried with backoff, so ExplainUserInfoError covers them
	// without a slow round trip here.
	refusals := []struct {
		name    string
		status  int
		message string
	}{
		{"unknown identity", http.StatusUnauthorized, "User not found"},
		{"whitelist refusal", http.StatusForbidden, "Your email is not allowed to sign in"},
		{"email conflict", http.StatusConflict, "Another account already uses this email"},
	}
	for _, tt := range refusals {
		t.Run(tt.name+" gives the server's message", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprintf(w, `{"error":true,"statusCode":%d,"statusMessage":%q,"message":%q}`, tt.status, tt.message, tt.message)
			}))
			defer server.Close()

			provider := &AuthProviderConfig{UserInfoEndpoint: server.URL}
			userinfo, err := confirmSignIn(provider, &TokenSet{AccessToken: "token"})
			if userinfo != nil {
				t.Errorf("userinfo = %+v, want nil", userinfo)
			}
			cliErr, ok := clierrors.AsCLIError(err)
			if !ok {
				t.Fatalf("error %v is not a *CLIError", err)
			}
			if cliErr.Message != tt.message {
				t.Errorf("Message = %q, want %q", cliErr.Message, tt.message)
			}
			if clierrors.GetExitCode(err) == 0 {
				t.Errorf("exit code is 0, want non-zero")
			}
		})
	}
}

func TestExplainUserInfoError(t *testing.T) {
	provider := &AuthProviderConfig{UserInfoEndpoint: "https://portal.example.com/api/external/userinfo"}

	tests := []struct {
		name           string
		err            error
		userType       UserType
		wantMessage    string
		wantSuggestion string // substring; empty means no suggestion
		wantDetail     string // substring of some detail; empty means not checked
		wantCause      bool   // the original error stays in the chain
	}{
		{
			name:           "unknown identity suggests signing in again",
			err:            &UserInfoError{StatusCode: 401, Status: "401 Unauthorized", Message: "User not found"},
			userType:       UserTypeHuman,
			wantMessage:    "User not found",
			wantSuggestion: "Sign in again with 'metaplay auth login'",
			wantDetail:     "401 Unauthorized",
		},
		{
			name:           "unknown machine user suggests machine-login",
			err:            &UserInfoError{StatusCode: 401, Status: "401 Unauthorized", Message: "User not found"},
			userType:       UserTypeMachine,
			wantMessage:    "User not found",
			wantSuggestion: "Sign in again with 'metaplay auth machine-login'",
		},
		{
			name:        "whitelist refusal is the server's message alone",
			err:         &UserInfoError{StatusCode: 403, Status: "403 Forbidden", Message: "Your email is not allowed to sign in"},
			userType:    UserTypeHuman,
			wantMessage: "Your email is not allowed to sign in",
			wantDetail:  "403 Forbidden",
		},
		{
			name:        "email conflict is the server's message alone",
			err:         &UserInfoError{StatusCode: 409, Status: "409 Conflict", Message: "Another account already uses this email"},
			userType:    UserTypeHuman,
			wantMessage: "Another account already uses this email",
			wantDetail:  "409 Conflict",
		},
		{
			name:        "refusal without a message falls back to the status",
			err:         &UserInfoError{StatusCode: 403, Status: "403 Forbidden"},
			userType:    UserTypeHuman,
			wantMessage: "Failed to fetch user info",
			wantCause:   true,
		},
		{
			name:           "unavailable server suggests trying again",
			err:            &UserInfoError{StatusCode: 503, Status: "503 Service Unavailable", Message: "Metaplay Auth is unreachable"},
			userType:       UserTypeHuman,
			wantMessage:    "Failed to fetch user info, the server is temporarily unavailable",
			wantSuggestion: "Try again in a moment",
			wantDetail:     "Metaplay Auth is unreachable",
		},
		{
			name:           "other server errors also suggest trying again",
			err:            &UserInfoError{StatusCode: 502, Status: "502 Bad Gateway", Message: "<html>Bad Gateway</html>"},
			userType:       UserTypeHuman,
			wantMessage:    "Failed to fetch user info, the server is temporarily unavailable",
			wantSuggestion: "Try again in a moment",
			wantDetail:     "502 Bad Gateway",
		},
		{
			name:        "other statuses keep the error as the cause",
			err:         &UserInfoError{StatusCode: 404, Status: "404 Not Found", Message: "no such endpoint"},
			userType:    UserTypeHuman,
			wantMessage: "Failed to fetch user info",
			wantCause:   true,
		},
		{
			name:        "network error keeps the error as the cause",
			err:         errors.New("failed to fetch userinfo: connection refused"),
			userType:    UserTypeHuman,
			wantMessage: "Failed to fetch user info",
			wantCause:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ExplainUserInfoError(tt.err, provider, tt.userType)

			cliErr, ok := clierrors.AsCLIError(err)
			if !ok {
				t.Fatalf("error %v is not a *CLIError", err)
			}
			if cliErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", cliErr.Message, tt.wantMessage)
			}
			if cliErr.IsUsageError() {
				t.Errorf("error is a usage error, want a runtime error")
			}
			if tt.wantSuggestion == "" && cliErr.Suggestion != "" {
				t.Errorf("Suggestion = %q, want none", cliErr.Suggestion)
			}
			if !strings.Contains(cliErr.Suggestion, tt.wantSuggestion) {
				t.Errorf("Suggestion = %q, want it to contain %q", cliErr.Suggestion, tt.wantSuggestion)
			}
			if tt.wantDetail != "" && !strings.Contains(strings.Join(cliErr.Details, "\n"), tt.wantDetail) {
				t.Errorf("Details = %q, want one to contain %q", cliErr.Details, tt.wantDetail)
			}
			if tt.wantCause != errors.Is(err, tt.err) {
				t.Errorf("errors.Is(err, cause) = %v, want %v", !tt.wantCause, tt.wantCause)
			}
		})
	}
}

func TestDescribeUser(t *testing.T) {
	tests := []struct {
		name     string
		userinfo UserInfoResponse
		want     string
	}{
		{"name and email", UserInfoResponse{Name: "Ada Lovelace", Email: "ada@example.com"}, "Ada Lovelace <ada@example.com>"},
		{"given and family name", UserInfoResponse{GivenName: "Ada", FamilyName: "Lovelace", Email: "ada@example.com"}, "Ada Lovelace <ada@example.com>"},
		{"email only", UserInfoResponse{Email: "ada@example.com"}, "<ada@example.com>"},
		{"name only", UserInfoResponse{Name: "Ada Lovelace"}, "Ada Lovelace"},
		{"blank name", UserInfoResponse{Name: "  ", Email: "ada@example.com"}, "<ada@example.com>"},
		{"subject only", UserInfoResponse{Subject: "abc-123"}, "abc-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeUser(&tt.userinfo); got != tt.want {
				t.Errorf("describeUser() = %q, want %q", got, tt.want)
			}
		})
	}
}
