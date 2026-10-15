/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFetchUserInfo_ReturnsUserInfoError(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		body             string
		wantMessage      string
		wantUnauthorized bool
	}{
		{
			name:             "unauthorized with explanation",
			status:           http.StatusUnauthorized,
			body:             `{"error":true,"statusCode":401,"statusMessage":"User not found with Metaplay Auth ID"}`,
			wantMessage:      "User not found with Metaplay Auth ID",
			wantUnauthorized: true,
		},
		{
			name:             "forbidden with empty body",
			status:           http.StatusForbidden,
			body:             "",
			wantMessage:      "",
			wantUnauthorized: true,
		},
		{
			name:             "not found",
			status:           http.StatusNotFound,
			body:             "no such endpoint",
			wantMessage:      "no such endpoint",
			wantUnauthorized: false,
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
			if userInfoErr.IsUnauthorized() != tt.wantUnauthorized {
				t.Errorf("IsUnauthorized() = %v, want %v", userInfoErr.IsUnauthorized(), tt.wantUnauthorized)
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
