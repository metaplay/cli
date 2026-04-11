/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metahttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metaplay/cli/pkg/auth"
)

// newTestClient returns a metahttp.Client pointed at the given test server.
func newTestClient(baseURL string) *Client {
	return NewJSONClient(&auth.TokenSet{AccessToken: "test-token"}, baseURL)
}

func TestRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	type response struct {
		Message string `json:"message"`
	}
	got, err := Get[response](newTestClient(server.URL), "/ping")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got.Message != "ok" {
		t.Errorf("expected message %q, got %q", "ok", got.Message)
	}
}

func TestRequest_HTTPError_400ParsedMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid shard index 5: environment has 1 shard(s)"}`))
	}))
	defer server.Close()

	type response struct{}
	_, err := Get[response](newTestClient(server.URL), "/things")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected StatusCode 400, got %d", httpErr.StatusCode)
	}
	if httpErr.Method != http.MethodGet {
		t.Errorf("expected Method GET, got %q", httpErr.Method)
	}
	if httpErr.URL != server.URL+"/things" {
		t.Errorf("expected URL %q, got %q", server.URL+"/things", httpErr.URL)
	}
	if httpErr.Message != "invalid shard index 5: environment has 1 shard(s)" {
		t.Errorf("expected parsed message, got %q", httpErr.Message)
	}
}

func TestRequest_HTTPError_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"snapshot not found: foo"}`))
	}))
	defer server.Close()

	type response struct{}
	_, err := Get[response](newTestClient(server.URL), "/missing")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected StatusCode 404, got %d", httpErr.StatusCode)
	}
	if httpErr.Message != "snapshot not found: foo" {
		t.Errorf("unexpected message: %q", httpErr.Message)
	}
}

func TestRequest_HTTPError_409(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"manual snapshot quota exceeded: 5/5 snapshots for this shard"}`))
	}))
	defer server.Close()

	type response struct{}
	_, err := PostJSON[response](newTestClient(server.URL), "/snapshots", map[string]any{"shardIndex": 0})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusConflict {
		t.Errorf("expected StatusCode 409, got %d", httpErr.StatusCode)
	}
	if httpErr.Method != http.MethodPost {
		t.Errorf("expected Method POST, got %q", httpErr.Method)
	}
	if httpErr.Message != "manual snapshot quota exceeded: 5/5 snapshots for this shard" {
		t.Errorf("unexpected message: %q", httpErr.Message)
	}
}

func TestRequest_HTTPError_500_NonJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream blew up\n"))
	}))
	defer server.Close()

	type response struct{}
	_, err := Get[response](newTestClient(server.URL), "/boom")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected StatusCode 500, got %d", httpErr.StatusCode)
	}
	// Non-JSON body should fall back to the raw body (trimmed).
	if httpErr.Message != "upstream blew up" {
		t.Errorf("expected fallback to raw body, got %q", httpErr.Message)
	}
}

func TestRequest_HTTPError_EmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	type response struct{}
	_, err := Get[response](newTestClient(server.URL), "/nothing")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadGateway {
		t.Errorf("expected StatusCode 502, got %d", httpErr.StatusCode)
	}
	if httpErr.Message != "" {
		t.Errorf("expected empty message for empty body, got %q", httpErr.Message)
	}
}

func TestHTTPError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *HTTPError
		want string
	}{
		{
			name: "with message",
			err:  &HTTPError{StatusCode: 409, Method: "POST", URL: "https://example.com/x", Message: "quota exceeded"},
			want: "POST https://example.com/x failed with status 409: quota exceeded",
		},
		{
			name: "without message",
			err:  &HTTPError{StatusCode: 502, Method: "GET", URL: "https://example.com/x"},
			want: "GET https://example.com/x failed with status 502",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseHTTPErrorMessage(t *testing.T) {
	tests := []struct {
		name           string
		body           []byte
		wantMsg        string
		wantStructured bool
	}{
		{"empty", []byte(""), "", false},
		{"nil", nil, "", false},
		{"json with error field", []byte(`{"error":"bad thing"}`), "bad thing", true},
		{"json without error field", []byte(`{"foo":"bar"}`), `{"foo":"bar"}`, false},
		{"non-json plain text", []byte("oops\n"), "oops", false},
		{"json with empty error", []byte(`{"error":""}`), `{"error":""}`, false},
		{"html from proxy", []byte("<html><body>502 Bad Gateway</body></html>"), "<html><body>502 Bad Gateway</body></html>", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotMsg, gotStructured := parseHTTPErrorMessage(tc.body)
			if gotMsg != tc.wantMsg {
				t.Errorf("msg: got %q, want %q", gotMsg, tc.wantMsg)
			}
			if gotStructured != tc.wantStructured {
				t.Errorf("structured: got %v, want %v", gotStructured, tc.wantStructured)
			}
		})
	}
}
