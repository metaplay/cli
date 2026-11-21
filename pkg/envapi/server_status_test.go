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

// A server whose certificate does not verify answered, so it is not summarized as one
// that does not respond.
func TestCheckHTTPServerInvalidCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler()) // Self-signed certificate
	defer server.Close()

	check := checkHTTPServer(t.Context(), server.URL)
	assert.True(t, check.Failed())
	assert.Equal(t, "has an invalid certificate", check.Summary)
}

func TestCheckDomain(t *testing.T) {
	// An IP address resolves to itself, without a lookup.
	check := checkDomain(t.Context(), "127.0.0.1")
	assert.Empty(t, check.Problems)
	assert.Equal(t, "resolves to 127.0.0.1", check.Summary)

	// The .invalid top-level domain never resolves (RFC 2606). Without a DNS server, the
	// lookup fails once the context is done.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	check = checkDomain(ctx, "server.invalid")
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

// Once the check has run out of time, the remaining connections are not made, and are
// reported as such rather than as failed connections.
func TestCheckClientEndpointOutOfTime(t *testing.T) {
	// A server that accepts a connection but never answers its TLS handshake. The
	// listener's backlog accepts the connection without an Accept.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port

	// The first connection hangs until the check runs out of time
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	check := checkClientEndpoint(ctx, "127.0.0.1", port)
	assert.Equal(t, "0/10 connections succeeded", check.Summary)
	if assert.Len(t, check.Problems, 2) {
		assert.Contains(t, check.Problems[0], "1 of 10 connections")
		assert.Contains(t, check.Problems[1], "9 of 10 connections")
		assert.Contains(t, check.Problems[1], "were not made")
	}
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

// captureLog sets non-interactive mode, in which a task's output lines are logged, and
// returns what is logged until the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logged bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&logged)
	tui.SetInteractiveMode(false)
	t.Cleanup(func() {
		log.Logger = previous
		tui.SetInteractiveMode(true)
	})
	return &logged
}

// newTestCheck returns a check named "Domain x" that waits up to timeout for probe to
// pass, with attempts a second apart.
func newTestCheck(timeout time.Duration, probe func(context.Context) ServerCheck) networkCheck {
	return networkCheck{name: "Domain x", waitingFor: "domain x to resolve", timeout: timeout, interval: time.Second, probe: probe}
}

// The wait retries a failed check every interval, and ends on the first pass, or on the
// first failure once its timeout has passed. It shows each failed attempt, and the
// summary of the check once it passes. See TestNetworkCheckWaitStopsOnCancel for a
// cancellation.
func TestNetworkCheckWait(t *testing.T) {
	notReady := ServerCheck{Summary: "does not resolve", Problems: []string{"no such host"}}

	t.Run("passes after retries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			logged := captureLog(t)
			runs := 0
			start := time.Now()
			err := newTestCheck(time.Minute, func(context.Context) ServerCheck {
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
			captureLog(t)
			runs := 0
			start := time.Now()
			err := newTestCheck(3*time.Second, func(context.Context) ServerCheck {
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
}

// A cancellation ends the wait for a check, here of an HTTP server, at once, with the
// cancellation as the error: both while a request is in flight and during the pause
// between two requests. The cancelled request is not logged as a failed attempt.
func TestNetworkCheckWaitStopsOnCancel(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"request in flight": func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
		"pause between requests": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(100*time.Millisecond, cancel)

			logged := captureLog(t)
			start := time.Now()
			err := newTestCheck(time.Minute, func(ctx context.Context) ServerCheck {
				return checkHTTPServer(ctx, server.URL)
			}).wait(ctx, &tui.TaskOutput{})

			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
			// The pause between requests is 1 second, which a cancelled wait must not sit out
			if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
				t.Errorf("returned %s after the start, want soon after the cancellation at 100ms", elapsed)
			}
			if strings.Contains(logged.String(), "context canceled") {
				t.Errorf("the cancelled request was logged as a connection error:\n%s", logged.String())
			}
		})
	}
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
	err, ok := clierrors.AsCLIError(status.Err())
	assert.True(t, ok)
	assert.Equal(t, "Game server is not ready", err.Message)
	assert.Equal(t, []string{"Pod all-0 (Starting): starting", "does not resolve"}, err.Details)
	assert.Equal(t, "Run this command again", err.Suggestion)

	assert.NoError(t, ServerStatus{Checks: []ServerCheck{{Name: "Pods"}}}.Err())
}

func TestNotReadySuggestion(t *testing.T) {
	targetEnv := &TargetEnvironment{HumanID: "nimbly"}
	failed := gameServerReadiness{failedPod: "all-0", failedPodLogSource: previousPodLogs}
	domainFailed := []ServerCheck{{Name: "Domain", Problems: []string{"no such host"}, hint: "domain hint"}, {Name: "Endpoint", Skipped: true}}
	serviceFailed := []ServerCheck{{Name: "Domain"}, {Name: "Endpoint", Problems: []string{"1 of 10 failed"}, hint: "service hint"}}
	tests := []struct {
		name      string
		podsErr   error
		readiness gameServerReadiness
		hasLogs   bool
		network   []ServerCheck
		want      string
	}{
		{name: "pods not read", podsErr: errors.New("forbidden"), want: "could not be read from Kubernetes"},
		{name: "never started", readiness: gameServerReadiness{failedPod: "all-0", failedPodLogSource: noPodLogs}, want: "pod all-0 did not start, so it has no logs"},
		{name: "failed with logs", readiness: failed, hasLogs: true, want: "Check the pod logs above for details, or run: metaplay debug logs nimbly"},
		{name: "failed, logs not fetched", readiness: failed, want: "Check the server logs with: metaplay debug logs nimbly"},
		{name: "starting", readiness: gameServerReadiness{problems: []string{"Pod all-0 (Starting): starting"}}, network: domainFailed, want: "run this command again in a minute"},
		{name: "domain fails", network: domainFailed, want: "domain hint"},
		{name: "service fails", network: serviceFailed, want: "service hint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, targetEnv.notReadySuggestion(tt.podsErr, tt.readiness, tt.hasLogs, tt.network), tt.want)
		})
	}
}
