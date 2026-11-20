/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/metaplay/cli/internal/tui"
)

func TestBuildHealthCheckPacket(t *testing.T) {
	packet := buildHealthCheckPacket()

	// Wire packet: [flags:1][payloadSize:3][payload:4]
	// flags = 0x04 (WirePacketType.HealthCheck), payloadSize = 4
	// payload = 0x00000001 (HealthCheckTypeBits.GlobalState)
	expected := []byte{0x04, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01}

	if len(packet) != len(expected) {
		t.Fatalf("expected packet length %d, got %d", len(expected), len(packet))
	}
	for i := range expected {
		if packet[i] != expected[i] {
			t.Errorf("byte %d: expected 0x%02x, got 0x%02x", i, expected[i], packet[i])
		}
	}
}

func TestParseProtocolHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		version byte
		status  byte
	}{
		{
			name:    "valid header",
			data:    []byte{0x4D, 0x50, 0x41, 0x53, 10, 3, 0x00, 0x00},
			wantErr: false,
			version: 10,
			status:  3,
		},
		{
			name:    "too short",
			data:    []byte{0x4D, 0x50, 0x41},
			wantErr: true,
		},
		{
			name:    "longer than 8 bytes (extra data ignored)",
			data:    []byte{0x4D, 0x50, 0x41, 0x53, 10, 3, 0x00, 0x00, 0xFF, 0xFF},
			wantErr: false,
			version: 10,
			status:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseProtocolHeader(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Version != tt.version {
				t.Errorf("version: expected %d, got %d", tt.version, info.Version)
			}
			if info.Status != tt.status {
				t.Errorf("status: expected %d, got %d", tt.status, info.Status)
			}
		})
	}
}

func TestValidateProtocolHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  protocolHeaderInfo
		wantErr bool
		errMsg  string
	}{
		{
			name: "ClusterRunning",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterRunning,
			},
			wantErr: false,
		},
		{
			name: "ClusterStarting",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterStarting,
			},
			wantErr: true,
			errMsg:  "server cluster is still starting",
		},
		{
			name: "ClusterShuttingDown",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterShuttingDown,
			},
			wantErr: true,
			errMsg:  "server cluster is shutting down",
		},
		{
			name: "wrong version",
			header: protocolHeaderInfo{
				Version: 5,
				Status:  protocolStatusClusterRunning,
			},
			wantErr: true,
			errMsg:  "unexpected protocol version",
		},
		{
			name: "unknown status",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  99,
			},
			wantErr: true,
			errMsg:  "unexpected protocol status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtocolHeader(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// A cancellation ends the wait for an HTTP server at once, with the cancellation as the error:
// both while a request is in flight and during the pause between two requests.
func TestWaitForHTTPServerToRespondStopsOnCancel(t *testing.T) {
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

			start := time.Now()
			err := waitForHTTPServerToRespond(ctx, &tui.TaskOutput{}, server.URL, time.Minute)

			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
			// The pause between requests is 1 second, which a cancelled wait must not sit out
			if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
				t.Errorf("returned %s after the start, want soon after the cancellation at 100ms", elapsed)
			}
		})
	}
}
