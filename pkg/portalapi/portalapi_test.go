/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package portalapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
)

func TestCanonicalizeSdkVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic padding
		{"36", "36.0.0"},
		{"36.1", "36.1.0"},
		{"36.1.0", "36.1.0"},

		// Already three segments
		{"1.2.3", "1.2.3"},
		{"100.200.300", "100.200.300"},

		// Prerelease
		{"36.1-beta", "36.1.0-beta"},
		{"36.1.0-rc.1", "36.1.0-rc.1"},

		// Metadata
		{"36.1.0+build123", "36.1.0+build123"},
		{"36.1.0-rc.1+build123", "36.1.0-rc.1+build123"},

		// Invalid input returned as-is
		{"not-a-version", "not-a-version"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := CanonicalizeSdkVersion(tc.input)
			if result != tc.expected {
				t.Errorf("CanonicalizeSdkVersion(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestIsMajorVersionOnly(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"34", true},
		{"0", true},
		{"100", true},
		{"34.1", false},
		{"34.1.0", false},
		{"abc", false},
		{"34a", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := IsMajorVersionOnly(tc.input)
			if result != tc.expected {
				t.Errorf("IsMajorVersionOnly(%q) = %v, expected %v", tc.input, result, tc.expected)
			}
		})
	}
}

// newTestClient creates a portalapi.Client pointing at the given test server.
func newTestClient(serverURL string) *Client {
	tokenSet := &auth.TokenSet{
		AccessToken: "test-access-token",
		TokenType:   "Bearer",
	}
	return &Client{
		httpClient: metahttp.NewJSONClient(tokenSet, serverURL, tokenSet.AccessToken),
		baseURL:    serverURL,
		tokenSet:   tokenSet,
	}
}

func TestExchangeTokenForEnvironment_Success(t *testing.T) {
	expectedToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.environment-scoped-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request path and query parameter.
		if r.URL.Path != "/api/v1/tokens/get-environment-access-token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("environment_human_id") != "lovely-wombats-build-nimbly" {
			t.Errorf("unexpected environment_human_id: %s", r.URL.Query().Get("environment_human_id"))
		}
		// Verify authorization header is set.
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header to be set")
		}

		// Portal returns the raw JWT string (metahttp.Get[string] uses response.String()).
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(expectedToken))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	token, err := client.ExchangeTokenForEnvironment("lovely-wombats-build-nimbly")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != expectedToken {
		t.Errorf("expected token %q, got %q", expectedToken, token)
	}
}

func TestExchangeTokenForEnvironment_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "access denied"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.ExchangeTokenForEnvironment("some-env")
	if err == nil {
		t.Fatal("expected error for server error response, got nil")
	}
}

func TestExchangeTokenForEnvironment_EmptyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return an empty body — no token content.
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.ExchangeTokenForEnvironment("some-env")
	if err == nil {
		t.Fatal("expected error for empty token response, got nil")
	}
}
