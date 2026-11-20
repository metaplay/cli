/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/stretchr/testify/assert"
)

func TestProbeHTTPServer(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantStatus string
		wantErr    bool
	}{
		{name: "success", statusCode: http.StatusOK, wantStatus: "200 OK"},
		{name: "login redirect", statusCode: http.StatusFound, wantStatus: "302 Found"},
		{name: "forbidden", statusCode: http.StatusForbidden, wantStatus: "403 Forbidden", wantErr: true},
		{name: "server error", statusCode: http.StatusBadGateway, wantStatus: "502 Bad Gateway", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "navigate", r.Header.Get("Sec-Fetch-Mode"))
				if tt.statusCode == http.StatusFound {
					// Not followed: the redirect itself is the response checked.
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			status, err := probeHTTPServer(context.Background(), server.URL)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func TestProbeHTTPServerNoResponse(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	status, err := probeHTTPServer(context.Background(), url)
	assert.Equal(t, "", status)
	assert.Error(t, err)

	check := checkHTTPServer(context.Background(), "Dashboard", url)
	assert.True(t, check.Failed())
	assert.Equal(t, "does not respond", check.Summary)
	assert.Len(t, check.Problems, 1)
}

func TestCheckDomain(t *testing.T) {
	check := checkDomain(context.Background(), "Domain", "localhost")
	assert.False(t, check.Failed(), check.Problems)
	assert.Equal(t, "Domain localhost", check.Name)
	assert.Empty(t, check.Problems)

	// The .invalid top-level domain never resolves (RFC 2606).
	check = checkDomain(context.Background(), "Domain", "server.invalid")
	assert.True(t, check.Failed())
	assert.Equal(t, "does not resolve", check.Summary)
	assert.Len(t, check.Problems, 1)
}

func TestCheckDomainThen(t *testing.T) {
	endpoint := ServerCheck{Name: "Endpoint", Problems: []string{"refused"}}

	domain, check := checkDomainThen(context.Background(), "Domain", "localhost", "Endpoint", func() ServerCheck { return endpoint })
	assert.False(t, domain.Failed())
	assert.Equal(t, endpoint, check)

	domain, check = checkDomainThen(context.Background(), "Domain", "server.invalid", "Endpoint", func() ServerCheck {
		t.Error("the check ran although the domain does not resolve")
		return endpoint
	})
	assert.True(t, domain.Failed())
	assert.True(t, check.Skipped)
	assert.Equal(t, "Endpoint", check.Name)
	assert.False(t, check.Failed(), "a skipped check adds no problem of its own")
}

func TestServerStatusErr(t *testing.T) {
	status := ServerStatus{
		Checks: []ServerCheck{
			{Name: "Pods", Problems: []string{"Pod all-0 (Starting): starting"}},
			{Name: "Domain", Problems: []string{"does not resolve"}},
			{Name: "Endpoint", Skipped: true},
			{Name: "Dashboard"},
		},
		suggestion: "Run this command again",
	}
	assert.Equal(t, []string{"Pod all-0 (Starting): starting", "does not resolve"}, status.Problems())
	err, ok := clierrors.AsCLIError(status.Err())
	assert.True(t, ok)
	assert.Equal(t, "Game server is not ready", err.Message)
	assert.Equal(t, status.Problems(), err.Details)
	assert.Equal(t, "Run this command again", err.Suggestion)

	assert.NoError(t, ServerStatus{Checks: []ServerCheck{{Name: "Pods"}}}.Err())
}
