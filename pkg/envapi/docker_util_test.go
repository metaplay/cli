/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	clierrors "github.com/metaplay/cli/internal/errors"
)

func TestIsRemoteImageNotFound(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "manifest unknown error code",
			err:  &transport.Error{Errors: []transport.Diagnostic{{Code: transport.ManifestUnknownErrorCode}}},
			want: true,
		},
		{
			name: "name unknown error code",
			err:  &transport.Error{Errors: []transport.Diagnostic{{Code: transport.NameUnknownErrorCode}}},
			want: true,
		},
		{
			name: "404 status without diagnostic code",
			err:  &transport.Error{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "wrapped not-found transport error",
			err:  fmt.Errorf("query failed: %w", &transport.Error{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "unauthorized is not a not-found",
			err:  &transport.Error{StatusCode: http.StatusUnauthorized, Errors: []transport.Diagnostic{{Code: transport.UnauthorizedErrorCode}}},
			want: false,
		},
		{
			name: "non-transport error",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRemoteImageNotFound(tc.err); got != tc.want {
				t.Errorf("isRemoteImageNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNewDockerClientUnsupportedVersion checks that only a daemon reporting an API version below
// the client's minimum is reported as unsupported, and not any daemon answering with HTTP 400.
func TestNewDockerClientUnsupportedVersion(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		wantMessage string
	}{
		{
			name: "old daemon",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("API-Version", "1.39")
				w.WriteHeader(http.StatusOK)
			},
			wantMessage: "Docker API version 1.39 is not supported",
		},
		{
			name: "HTTP 400 without a version, such as a TLS daemon reached over plain HTTP",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "Client sent an HTTP request to an HTTPS server.", http.StatusBadRequest)
			},
			wantMessage: "Cannot connect to Docker",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
			t.Setenv("DOCKER_API_VERSION", "")
			t.Setenv("DOCKER_TLS_VERIFY", "")
			t.Setenv("DOCKER_CERT_PATH", "")

			dockerClient, err := NewDockerClient(context.Background(), func(string) {})
			if err == nil {
				_ = dockerClient.Close()
				t.Fatal("NewDockerClient succeeded, want an error")
			}
			cliErr, ok := errors.AsType[*clierrors.CLIError](err)
			if !ok {
				t.Fatalf("error is not a CLIError: %v", err)
			}
			if cliErr.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", cliErr.Message, tt.wantMessage)
			}
		})
	}
}
