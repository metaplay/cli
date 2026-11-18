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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

// shortenDockerWait makes WaitForDocker's timings short for a test.
func shortenDockerWait(t *testing.T, timeout, slowAfter, stillWaitingEvery time.Duration) {
	t.Helper()
	oldTimeout, oldSlowAfter, oldEvery := dockerResponseTimeout, dockerSlowAfter, dockerStillWaitingEvery
	dockerResponseTimeout, dockerSlowAfter, dockerStillWaitingEvery = timeout, slowAfter, stillWaitingEvery
	t.Cleanup(func() {
		dockerResponseTimeout, dockerSlowAfter, dockerStillWaitingEvery = oldTimeout, oldSlowAfter, oldEvery
	})
}

// blockForever is a check that ignores its context, as a hung syscall would. It returns when the
// test ends.
func blockForever(t *testing.T) func(context.Context) error {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(context.Context) error {
		<-release
		return nil
	}
}

// waitingRecorder records WaitForDocker's progress messages.
type waitingRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (r *waitingRecorder) onWaiting(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, message)
}

func (r *waitingRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.messages)
}

func TestWaitForDockerTimeout(t *testing.T) {
	shortenDockerWait(t, 50*time.Millisecond, time.Hour, time.Hour)

	start := time.Now()
	err := WaitForDocker(context.Background(), func(string) {}, blockForever(t))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %v, want right after the 50ms deadline even though check does not stop", elapsed)
	}
	if !errors.Is(err, ErrDockerTimeout) {
		t.Fatalf("error = %v, want ErrDockerTimeout in its chain", err)
	}
	if cliErr, ok := errors.AsType[*clierrors.CLIError](err); !ok || !strings.HasPrefix(cliErr.Message, "Docker did not respond within") {
		t.Errorf("error = %#v, want a CLIError saying Docker did not respond", err)
	}
}

func TestWaitForDockerCheckDeadlineError(t *testing.T) {
	shortenDockerWait(t, 50*time.Millisecond, time.Hour, time.Hour)

	// A check that honors its context fails with the deadline, which is reported as the timeout.
	err := WaitForDocker(context.Background(), func(string) {}, func(ctx context.Context) error {
		<-ctx.Done()
		return fmt.Errorf("ping: %w", ctx.Err())
	})
	if !errors.Is(err, ErrDockerTimeout) {
		t.Errorf("error = %v, want ErrDockerTimeout in its chain", err)
	}
}

func TestWaitForDockerCancelled(t *testing.T) {
	shortenDockerWait(t, time.Hour, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err := WaitForDocker(ctx, func(string) {}, blockForever(t))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrDockerTimeout) {
		t.Error("cancellation reported as a timeout")
	}
}

func TestWaitForDockerCheckError(t *testing.T) {
	shortenDockerWait(t, time.Hour, time.Hour, time.Hour)

	checkErr := errors.New("connection refused")
	if err := WaitForDocker(context.Background(), func(string) {}, func(context.Context) error { return checkErr }); err != checkErr { //nolint:errorlint // Must be returned as is
		t.Errorf("error = %v, want check's error as is", err)
	}
	if err := WaitForDocker(context.Background(), func(string) {}, func(context.Context) error { return nil }); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}

func TestWaitForDockerProgress(t *testing.T) {
	shortenDockerWait(t, 200*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond)

	var recorder waitingRecorder
	_ = WaitForDocker(context.Background(), recorder.onWaiting, blockForever(t))
	messages := recorder.get()
	if len(messages) < 2 {
		t.Fatalf("messages = %q, want a waiting message and at least one still-waiting message", messages)
	}
	if messages[0] != "Waiting for Docker to respond..." {
		t.Errorf("first message = %q", messages[0])
	}
	for _, message := range messages[1:] {
		if message != "Still waiting for Docker to respond..." {
			t.Errorf("later message = %q", message)
		}
	}

	// No reports after return.
	time.Sleep(120 * time.Millisecond)
	if after := recorder.get(); len(after) != len(messages) {
		t.Errorf("onWaiting called %d times after WaitForDocker returned", len(after)-len(messages))
	}
}

// TestReadLocalDockerImagesByProjectIDInspectErrors checks that an image removed while listing is
// skipped, and that any other inspect failure fails the listing instead of returning a partial list.
func TestReadLocalDockerImagesByProjectIDInspectErrors(t *testing.T) {
	tests := []struct {
		name          string
		inspectStatus int
		wantErr       bool
	}{
		{name: "image removed", inspectStatus: http.StatusNotFound},
		{name: "daemon error", inspectStatus: http.StatusInternalServerError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("API-Version", "1.47")
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/_ping"):
					_, _ = w.Write([]byte("OK"))
				case strings.HasSuffix(r.URL.Path, "/images/json"):
					_, _ = w.Write([]byte(`[{"Id":"sha256:abc","RepoTags":["game:1","game:2"]}]`))
				default:
					w.WriteHeader(tt.inspectStatus)
					_, _ = w.Write([]byte(`{"message":"inspect failed"}`))
				}
			}))
			defer server.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
			t.Setenv("DOCKER_API_VERSION", "")
			t.Setenv("DOCKER_TLS_VERIFY", "")
			t.Setenv("DOCKER_CERT_PATH", "")

			images, err := ReadLocalDockerImagesByProjectID(context.Background(), "game")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %d images and no error, want an error", len(images))
				}
				return
			}
			if err != nil || len(images) != 0 {
				t.Errorf("got %d images, error %v; want none and no error", len(images), err)
			}
		})
	}
}
