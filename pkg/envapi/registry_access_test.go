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

	clierrors "github.com/metaplay/cli/internal/errors"
)

// fakeRegistry answers the registry API's version check with 200, and manifest and blob upload
// requests with status and, if code is set, a registry error with that code.
func fakeRegistry(t *testing.T, status int, code string) *DockerCredentials {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if status == http.StatusAccepted { // Blob upload started: tell the client where
			w.Header().Set("Location", "/v2/game/blobs/uploads/1")
		}
		if code != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"errors":[{"code":%q,"message":"registry says %s"}]}`, code, code)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return &DockerCredentials{RegistryURL: strings.TrimPrefix(server.URL, "http://"), Username: "user", Password: "secret"}
}

func TestCheckRegistryAccess(t *testing.T) {
	tests := []struct {
		name   string
		push   bool
		status int
		code   string
		want   RegistryAccess
	}{
		{name: "pull allowed", status: http.StatusOK, want: RegistryAccessUnknown},
		{name: "pull unauthorized", status: http.StatusUnauthorized, code: "UNAUTHORIZED", want: RegistryAccessDenied},
		{name: "pull forbidden without a body", status: http.StatusForbidden, want: RegistryAccessDenied},
		{name: "pull missing repository", status: http.StatusNotFound, code: "NAME_UNKNOWN", want: RegistryRepositoryNotFound},
		{name: "pull missing tag", status: http.StatusNotFound, code: "MANIFEST_UNKNOWN", want: RegistryImageNotFound},
		{name: "pull registry error", status: http.StatusInternalServerError, want: RegistryAccessUnknown},
		{name: "push allowed", push: true, status: http.StatusAccepted, want: RegistryAccessUnknown},
		{name: "push denied", push: true, status: http.StatusForbidden, code: "DENIED", want: RegistryAccessDenied},
		{name: "push missing repository", push: true, status: http.StatusNotFound, code: "NAME_UNKNOWN", want: RegistryRepositoryNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := fakeRegistry(t, tt.status, tt.code)
			if got := CheckRegistryAccess(context.Background(), creds, creds.RegistryURL+"/game:1", tt.push); got != tt.want {
				t.Errorf("CheckRegistryAccess = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestFetchRemoteDockerImageDigestsAccessDenied checks that a registry rejecting the credentials is
// explained with the credentials suggestion, since push checks the remote tag first.
func TestFetchRemoteDockerImageDigestsAccessDenied(t *testing.T) {
	creds := fakeRegistry(t, http.StatusForbidden, "DENIED")
	_, _, err := FetchRemoteDockerImageDigests(creds, creds.RegistryURL+"/game:1")
	cliErr, ok := errors.AsType[*clierrors.CLIError](err)
	if !ok || cliErr.Suggestion != RegistryCredentialsSuggestion {
		t.Errorf("error = %v, want a CLIError with the credentials suggestion", err)
	}
}
