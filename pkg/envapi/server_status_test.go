/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
	assert.Equal(t, CheckFailed, check.Result)
	assert.Equal(t, "does not respond", check.Summary)
	assert.Len(t, check.Problems, 1)
}

func TestCheckDomain(t *testing.T) {
	check := checkDomain(context.Background(), "Domain", "localhost")
	assert.Equal(t, CheckPassed, check.Result, check.Problems)
	assert.Equal(t, "Domain localhost", check.Name)
	assert.Empty(t, check.Problems)

	// The .invalid top-level domain never resolves (RFC 2606).
	check = checkDomain(context.Background(), "Domain", "server.invalid")
	assert.Equal(t, CheckFailed, check.Result)
	assert.Equal(t, "does not resolve", check.Summary)
	assert.Len(t, check.Problems, 1)
}

func TestCheckAfterDomain(t *testing.T) {
	passed := ServerCheck{Name: "Domain", Result: CheckPassed}
	failed := ServerCheck{Name: "Domain", Result: CheckFailed, Problems: []string{"does not resolve"}}
	endpoint := ServerCheck{Name: "Endpoint", Result: CheckFailed, Problems: []string{"refused"}}

	assert.Equal(t, endpoint, checkAfterDomain(passed, "Endpoint", func() ServerCheck { return endpoint }))

	skipped := checkAfterDomain(failed, "Endpoint", func() ServerCheck {
		t.Error("the check ran although the domain does not resolve")
		return endpoint
	})
	assert.Equal(t, CheckSkipped, skipped.Result)
	assert.Equal(t, "Endpoint", skipped.Name)
	assert.Empty(t, skipped.Problems, "a skipped check adds no problem of its own")
}

func TestServerStatusProblems(t *testing.T) {
	status := ServerStatus{Checks: []ServerCheck{
		{Name: "Pods", Result: CheckFailed, Problems: []string{"Pod all-0 (Starting): starting"}},
		{Name: "Domain", Result: CheckFailed, Problems: []string{"does not resolve"}},
		{Name: "Endpoint", Result: CheckSkipped},
		{Name: "Dashboard", Result: CheckPassed},
	}}
	assert.Equal(t, []string{"Pod all-0 (Starting): starting", "does not resolve"}, status.Problems())
	assert.Empty(t, ServerStatus{Checks: []ServerCheck{{Name: "Pods", Result: CheckPassed}}}.Problems())
}
