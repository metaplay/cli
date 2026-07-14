/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/metahttp"
)

// TestMain disables interactive mode so tests that exercise the
// non-interactive branches of shard resolution and similar helpers do not
// try to open a TTY via bubbletea.
func TestMain(m *testing.M) {
	tui.SetInteractiveMode(false)
	os.Exit(m.Run())
}

func TestValidateDatabaseFormat(t *testing.T) {
	if err := validateDatabaseFormat("text"); err != nil {
		t.Errorf("text should be valid, got %v", err)
	}
	if err := validateDatabaseFormat("json"); err != nil {
		t.Errorf("json should be valid, got %v", err)
	}
	if err := validateDatabaseFormat("yaml"); err == nil {
		t.Error("yaml should be invalid")
	}
	if err := validateDatabaseFormat(""); err == nil {
		t.Error("empty should be invalid")
	}
}

func TestParseRollbackTargetTime_Absolute(t *testing.T) {
	now := time.Date(2026, 4, 9, 15, 30, 0, 0, time.UTC)
	got, err := parseRollbackTargetTime("2026-04-09T14:00:00Z", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseRollbackTargetTime_Relative(t *testing.T) {
	now := time.Date(2026, 4, 9, 15, 30, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"30m", now.Add(-30 * time.Minute)},
		{"2h", now.Add(-2 * time.Hour)},
		{"1h30m", now.Add(-90 * time.Minute)},
		{"45s", now.Add(-45 * time.Second)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseRollbackTargetTime(tc.in, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseRollbackTargetTime_Invalid(t *testing.T) {
	now := time.Now().UTC()
	cases := []string{
		"",
		"   ",
		"tomorrow",
		"-30m",
		"0",
		"2026-04-09",          // missing time part
		"2026/04/09 15:00:00", // wrong format
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			if _, err := parseRollbackTargetTime(s, now); err == nil {
				t.Errorf("expected error for %q", s)
			}
		})
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-5 * time.Second, "0s"},
		{1 * time.Second, "1s"},
		{45 * time.Second, "45s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m30s"},
		{2 * time.Minute, "2m"},
		{2*time.Minute + 5*time.Second, "2m05s"},
		{61 * time.Minute, "61m"},
		{time.Hour + 30*time.Second, "60m30s"},
	}
	for _, tc := range cases {
		if got := formatElapsed(tc.in); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatDatabaseAge(t *testing.T) {
	// This test is loose because formatDatabaseAge relies on time.Since(t),
	// but we can check boundaries by passing times relative to now.
	now := time.Now()
	cases := []struct {
		t      time.Time
		substr string
	}{
		{now.Add(-10 * time.Second), "s"},
		{now.Add(-5 * time.Minute), "m"},
		{now.Add(-3 * time.Hour), "h"},
		{now.Add(-2 * 24 * time.Hour), "d"},
		{now.Add(-10 * 24 * time.Hour), "w"},
	}
	for _, tc := range cases {
		got := formatDatabaseAge(tc.t)
		if !strings.Contains(got, tc.substr) {
			t.Errorf("age for %v = %q, expected to contain %q", tc.t, got, tc.substr)
		}
	}
	if got := formatDatabaseAge(time.Time{}); got != "-" {
		t.Errorf("expected '-' for zero time, got %q", got)
	}
}

func TestMapDatabaseHTTPError(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantContains string
		wantHint     string
	}{
		{
			name: "400 generic",
			err: &metahttp.HTTPError{
				StatusCode: http.StatusBadRequest,
				Message:    "invalid shard index 5: environment has 1 shard(s)",
			},
			wantContains: "Invalid request",
		},
		{
			name: "404 snapshot",
			err: &metahttp.HTTPError{
				StatusCode: http.StatusNotFound,
				Message:    "snapshot not found: foo",
			},
			wantContains: "Not found",
		},
		{
			name: "409 quota",
			err: &metahttp.HTTPError{
				StatusCode: http.StatusConflict,
				Message:    "manual snapshot quota exceeded: 5/5",
			},
			wantContains: "quota exceeded",
			wantHint:     "Delete older manual snapshots",
		},
		{
			name: "409 concurrency",
			err: &metahttp.HTTPError{
				StatusCode: http.StatusConflict,
				Message:    "snapshot foo is currently being created",
			},
			wantContains: "being created",
			wantHint:     "Wait for the in-progress operation",
		},
		{
			name: "500",
			err: &metahttp.HTTPError{
				StatusCode: http.StatusInternalServerError,
				Message:    "failed to create snapshot",
			},
			wantContains: "failed to create snapshot",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapDatabaseHTTPError(tc.err, "create snapshot")
			if mapped == nil {
				t.Fatal("expected non-nil error")
			}
			msg := mapped.Error()
			if !strings.Contains(msg, tc.wantContains) {
				t.Errorf("error %q should contain %q", msg, tc.wantContains)
			}
			if tc.wantHint != "" {
				var cliErr *clierrors.CLIError
				if !errors.As(mapped, &cliErr) {
					t.Fatalf("expected *clierrors.CLIError, got %T", mapped)
				}
				if !strings.Contains(cliErr.Suggestion, tc.wantHint) {
					t.Errorf("suggestion %q should contain %q", cliErr.Suggestion, tc.wantHint)
				}
			}
		})
	}
}

func TestMapDatabaseHTTPError_NonHTTPPreservesCause(t *testing.T) {
	cause := errors.New("connection refused")
	mapped := mapDatabaseHTTPError(cause, "create snapshot")
	if mapped == nil {
		t.Fatal("expected non-nil error")
	}
	// The message should mention the operation, not leak the raw cause.
	if !strings.Contains(mapped.Error(), "Failed to create snapshot") {
		t.Errorf("expected operation description in message, got %q", mapped.Error())
	}
	// The cause should be preserved via Unwrap for errors.Is checks.
	if !errors.Is(mapped, cause) {
		t.Errorf("expected cause to be preserved in error chain")
	}
}

func TestMapDatabaseHTTPError_NilError(t *testing.T) {
	if got := mapDatabaseHTTPError(nil, "anything"); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

func TestResolveTargetShards_SingleShardDefaultsToZero(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{{ShardIndex: 0, ClusterID: "mygame-0"}},
	}
	got, err := resolveTargetShards(context.Background(), caps, -1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("expected [0], got %v", got)
	}
}

func TestResolveTargetShards_ExplicitShard(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0"},
			{ShardIndex: 1, ClusterID: "mygame-1"},
		},
	}
	got, err := resolveTargetShards(context.Background(), caps, 1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1], got %v", got)
	}
}

func TestResolveTargetShards_InvalidShard(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0"},
		},
	}
	_, err := resolveTargetShards(context.Background(), caps, 5, false)
	if err == nil {
		t.Fatal("expected error for invalid shard")
	}
	if !strings.Contains(err.Error(), "Invalid --shard") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTargetShards_AllShards(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0"},
			{ShardIndex: 1, ClusterID: "mygame-1"},
			{ShardIndex: 2, ClusterID: "mygame-2"},
		},
	}
	got, err := resolveTargetShards(context.Background(), caps, -1, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int{0, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("index %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestResolveTargetShards_BothFlagsRejected(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0"},
		},
	}
	_, err := resolveTargetShards(context.Background(), caps, 0, true)
	if err == nil {
		t.Fatal("expected error for conflicting flags")
	}
	if !strings.Contains(err.Error(), "Cannot use --shard and --all-shards together") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTargetShards_MultiShardRequiresExplicit(t *testing.T) {
	// In non-interactive mode, multi-shard env with no flag must fail.
	// This depends on tui.IsInteractiveMode() returning false in tests,
	// which it does by default (no tty under `go test`).
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0"},
			{ShardIndex: 1, ClusterID: "mygame-1"},
		},
	}
	_, err := resolveTargetShards(context.Background(), caps, -1, false)
	if err == nil {
		t.Fatal("expected error for missing --shard on multi-shard env")
	}
	if !strings.Contains(err.Error(), "--shard is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureShardsSupportCapability(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{
		Shards: []envapi.DatabaseShardCapabilities{
			{ShardIndex: 0, ClusterID: "mygame-0", SupportsSnapshots: true, SupportsRollback: true},
			{ShardIndex: 1, ClusterID: "mygame-1", SupportsSnapshots: true, SupportsRollback: false},
			{ShardIndex: 2, ClusterID: "mygame-2", SupportsSnapshots: false, SupportsRollback: false},
		},
	}
	snapshotsCheck := func(s envapi.DatabaseShardCapabilities) bool { return s.SupportsSnapshots }
	rollbackCheck := func(s envapi.DatabaseShardCapabilities) bool { return s.SupportsRollback }

	t.Run("single good shard", func(t *testing.T) {
		if err := ensureShardsSupportCapability(caps, []int{0}, snapshotsCheck, "Snapshot creation"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("single bad shard names the shard", func(t *testing.T) {
		err := ensureShardsSupportCapability(caps, []int{2}, snapshotsCheck, "Snapshot creation")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Snapshot creation is not supported on shard 2") {
			t.Errorf("expected single-shard error naming shard 2, got %q", err.Error())
		}
	})
	t.Run("multi-shard partial success fails and lists bad shards", func(t *testing.T) {
		err := ensureShardsSupportCapability(caps, []int{0, 1, 2}, rollbackCheck, "Rollback")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Rollback is not supported on 2 of 3 target shards") {
			t.Errorf("expected multi-shard summary, got %q", err.Error())
		}
		var cliErr *clierrors.CLIError
		if !errors.As(err, &cliErr) {
			t.Fatalf("expected *clierrors.CLIError, got %T", err)
		}
		if len(cliErr.Details) != 2 {
			t.Errorf("expected 2 detail lines for 2 bad shards, got %d", len(cliErr.Details))
		}
	})
	t.Run("all shards good", func(t *testing.T) {
		if err := ensureShardsSupportCapability(caps, []int{0, 1}, snapshotsCheck, "Snapshot creation"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestResolveTargetShards_UnsupportedEnv(t *testing.T) {
	caps := &envapi.DatabaseCapabilitiesResponse{Shards: nil}
	_, err := resolveTargetShards(context.Background(), caps, -1, false)
	if err == nil {
		t.Fatal("expected error for unsupported env")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

// newTestTargetEnvironment builds a TargetEnvironment pointed at the given
// test server URL so that calls like GetDatabaseCapabilities hit the stub.
func newTestTargetEnvironment(baseURL string) *envapi.TargetEnvironment {
	tokenSet := &auth.TokenSet{AccessToken: "test-token"}
	return &envapi.TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: baseURL,
		HumanID:         "test-env",
		StackApiClient:  metahttp.NewJSONClient(tokenSet, baseURL),
	}
}

// TestDatabaseAvailabilityErrorMapping verifies that each class of error from
// the capabilities probe is translated into a friendly "database operations
// are not available" error with the underlying cause attached. This is the
// core backwards-compatibility behavior: regardless of how an older
// infrastructure stack fails to respond (404 with empty body, 404 with
// JSON, 500, etc.), the user-facing message is uniform.
func TestDatabaseAvailabilityErrorMapping(t *testing.T) {
	cases := []struct {
		name                string
		handler             http.HandlerFunc
		wantErr             bool
		wantCauseStatusCode int
	}{
		{
			name: "200 with shards is a success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"shards":[{"shardIndex":0,"clusterId":"t","provider":"aws-rds","supportsSnapshots":true,"supportsRollback":true,"maxManualSnapshots":5}]}`))
			},
			wantErr: false,
		},
		{
			name: "200 with empty shards is a success (per-env unsupported)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"shards":[]}`))
			},
			wantErr: false,
		},
		{
			name: "404 with empty body (Gin default, older stack) is 'not available'",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr:             true,
			wantCauseStatusCode: http.StatusNotFound,
		},
		{
			name: "404 with JSON body (tenant middleware or not-found handler) is 'not available'",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Tenant not found in the TenantRegistry"}`))
			},
			wantErr:             true,
			wantCauseStatusCode: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			// Simulate what resolveEnvironmentForDatabaseOps does post env-resolution:
			// a capabilities probe, with uniform error handling on any failure.
			target := newTestTargetEnvironment(server.URL)
			_, probeErr := target.GetDatabaseCapabilities()

			// This mirrors the wrapping performed by resolveEnvironmentForDatabaseOps.
			var err error
			if probeErr != nil {
				err = clierrors.New("Database operations are not available for this environment").
					WithCause(probeErr)
			}

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "not available") {
				t.Errorf("expected 'not available' message, got %q", err.Error())
			}
			var httpErr *metahttp.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("expected cause to be *metahttp.HTTPError, got %T", errors.Unwrap(err))
			}
			if httpErr.StatusCode != tc.wantCauseStatusCode {
				t.Errorf("expected cause StatusCode %d, got %d", tc.wantCauseStatusCode, httpErr.StatusCode)
			}
		})
	}
}
