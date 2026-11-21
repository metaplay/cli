/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	check := checkHTTPServer(context.Background(), url)
	assert.True(t, check.Failed())
	assert.Equal(t, "does not respond", check.Summary)
	assert.Len(t, check.Problems, 1)
	assert.Equal(t, 1, strings.Count(check.Problems[0], url), "the URL is named once: %s", check.Problems[0])
}

func TestCheckDomain(t *testing.T) {
	check := checkDomain(context.Background(), "localhost")
	assert.False(t, check.Failed(), check.Problems)
	assert.Empty(t, check.Problems)

	// The .invalid top-level domain never resolves (RFC 2606).
	check = checkDomain(context.Background(), "server.invalid")
	assert.True(t, check.Failed())
	assert.Equal(t, "does not resolve", check.Summary)
	assert.Len(t, check.Problems, 1)
}

// All connections are made and counted, also after one has failed.
func TestCheckClientEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close() // Connections to the port are now refused

	check := checkClientEndpoint(t.Context(), "127.0.0.1", port)
	assert.Equal(t, "0/10 connections succeeded", check.Summary)
	assert.Len(t, check.Problems, 1)
	assert.Contains(t, check.Problems[0], "10 of 10 connections")
}

// The service at a domain is checked after the domain, and skipped if the domain does
// not resolve.
func TestDomainChecksCheckOnce(t *testing.T) {
	passing := func(context.Context) ServerCheck { return ServerCheck{Summary: "ok"} }
	failing := func(context.Context) ServerCheck {
		return ServerCheck{Summary: "does not resolve", Problems: []string{"no such host"}}
	}
	notRun := func(context.Context) ServerCheck {
		t.Error("the service was checked although its domain does not resolve")
		return ServerCheck{}
	}

	results := domainChecks{
		domain:  networkCheck{name: "Domain", probe: passing},
		service: networkCheck{name: "Endpoint", probe: passing},
	}.checkOnce(t.Context())
	assert.Equal(t, []ServerCheck{{Name: "Domain", Summary: "ok"}, {Name: "Endpoint", Summary: "ok"}}, results)

	results = domainChecks{
		domain:  networkCheck{name: "Domain", probe: failing},
		service: networkCheck{name: "Dashboard", probe: notRun},
	}.checkOnce(t.Context())
	assert.Len(t, results, 2)
	domain, service := results[0], results[1]
	assert.True(t, domain.Failed())
	assert.Equal(t, "Dashboard", service.Name)
	assert.True(t, service.Skipped)
	assert.False(t, service.Failed(), "a skipped check adds no problem of its own")
}

// The wait retries a failed check every interval, and ends on the first pass, on the
// first failure once its timeout has passed, or at once when the context is done. It
// shows each failed attempt, and the summary of the check once it passes.
func TestNetworkCheckWait(t *testing.T) {
	// With no display, the wait's output lines are logged
	tui.SetInteractiveMode(false)
	t.Cleanup(func() { tui.SetInteractiveMode(true) })
	notReady := ServerCheck{Summary: "does not resolve", Problems: []string{"no such host"}}
	newCheck := func(timeout time.Duration, probe func(context.Context) ServerCheck) networkCheck {
		return networkCheck{name: "Domain x", waitingFor: "domain x to resolve", timeout: timeout, interval: time.Second, probe: probe}
	}

	t.Run("passes after retries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var logged bytes.Buffer
			previous := log.Logger
			log.Logger = zerolog.New(&logged)
			t.Cleanup(func() { log.Logger = previous })
			runs := 0
			start := time.Now()
			err := newCheck(time.Minute, func(context.Context) ServerCheck {
				runs++
				if runs < 3 {
					return notReady
				}
				return ServerCheck{Summary: "resolves to 10.0.0.1"}
			}).wait(t.Context(), &tui.TaskOutput{})
			assert.NoError(t, err)
			assert.Equal(t, 3, runs)
			assert.Equal(t, 2*time.Second, time.Since(start))
			assert.Contains(t, logged.String(), "Attempt 1 failed, retrying: no such host")
			assert.Contains(t, logged.String(), "Attempt 2 failed, retrying: no such host")
			assert.Contains(t, logged.String(), "Domain x: resolves to 10.0.0.1")
		})
	})

	t.Run("times out", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runs := 0
			start := time.Now()
			err := newCheck(3*time.Second, func(context.Context) ServerCheck {
				runs++
				return notReady
			}).wait(t.Context(), &tui.TaskOutput{})
			cliErr, ok := clierrors.AsCLIError(err)
			if assert.True(t, ok, "error: %v", err) {
				assert.Equal(t, "Timed out after 3s waiting for domain x to resolve", cliErr.Message)
				assert.Equal(t, notReady.Problems, cliErr.Details)
			}
			// Attempts at 0s, 1s, 2s and 3s: the one at the timeout is the last
			assert.Equal(t, 4, runs)
			assert.Equal(t, 3*time.Second, time.Since(start))
		})
	})

	t.Run("stops on cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(1500*time.Millisecond, cancel)
			start := time.Now()
			err := newCheck(time.Minute, func(context.Context) ServerCheck { return notReady }).wait(ctx, &tui.TaskOutput{})
			assert.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1500*time.Millisecond, time.Since(start))
		})
	})
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

func TestNotReadySuggestion(t *testing.T) {
	targetEnv := &TargetEnvironment{HumanID: "nimbly"}
	failed := gameServerReadiness{failedPod: "all-0", failedPodLogs: previousPodLogs}
	tests := []struct {
		name      string
		podsErr   error
		readiness gameServerReadiness
		hasLogs   bool
		want      string
	}{
		{name: "pods not read", podsErr: errors.New("forbidden"), want: "could not be read from Kubernetes"},
		{name: "never started", readiness: gameServerReadiness{failedPod: "all-0", failedPodLogs: noPodLogs}, want: "pod all-0 did not start, so it has no logs"},
		{name: "failed with logs", readiness: failed, hasLogs: true, want: "Check the pod logs above for details, or run: metaplay debug logs nimbly"},
		{name: "failed, logs not fetched", readiness: failed, want: "Check the server logs with: metaplay debug logs nimbly"},
		{name: "starting", readiness: gameServerReadiness{problems: []string{"Pod all-0 (Starting): starting"}}, want: "run this command again in a minute"},
		{name: "network only", want: "DNS or the load balancer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, targetEnv.notReadySuggestion(tt.podsErr, tt.readiness, tt.hasLogs), tt.want)
		})
	}
}
