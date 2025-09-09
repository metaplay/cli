package testutil

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/docker/go-connections/nat"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// GameServerOptions configures the server container and the poller behavior.
type GameServerOptions struct {
	Image         string        // e.g. "myorg/myserver:latest"
	Port          string        // container port like "8080/tcp"
	HealthPath    string        // e.g. "/health"
	MetricsPath   string        // e.g. "/metrics"
	PollInterval  time.Duration // how often to collect metrics
	HistoryLimit  int           // max samples kept in memory (0 or <0 => unbounded)
	Env           map[string]string
	ExposedPorts  []string // optional override; defaults to []string{Port}
	ContainerName string   // optional; useful in CI logs
}

// MetricSample is a single metrics collection result.
type MetricSample struct {
	At       time.Time
	Raw      string // Placeholder: raw response or serialized snapshot
	Err      error  // Non-nil if collection failed
	Duration time.Duration
}

// BackgroundGameServer wraps a running container with a background metrics collector.
type BackgroundGameServer struct {
	opts GameServerOptions

	ctr        tc.Container
	baseURL    *url.URL
	mu         sync.RWMutex
	history    []MetricSample
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	collecting bool
}

// New creates a wrapper with the given options (does not start the container).
func New(opts GameServerOptions) *BackgroundGameServer {
	if len(opts.ExposedPorts) == 0 && opts.Port != "" {
		opts.ExposedPorts = []string{opts.Port}
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.HistoryLimit < 0 {
		opts.HistoryLimit = 0
	}
	return &BackgroundGameServer{opts: opts}
}

// Start launches the server container, waits for readiness, and starts metrics collection.
func (s *BackgroundGameServer) Start(ctx context.Context) error {
	// Build container request
	req := tc.ContainerRequest{
		Image:        s.opts.Image,
		Name:         s.opts.ContainerName,
		ExposedPorts: s.opts.ExposedPorts,
		Env:          s.opts.Env,
		WaitingFor: wait.ForHTTP(s.opts.HealthPath).
			WithPort(nat.Port(s.opts.Port)).
			WithStatusCodeMatcher(func(code int) bool { return code == 200 }).
			WithStartupTimeout(2 * time.Minute),
	}

	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	s.ctr = ctr

	// Discover host:port where we can reach the server from the host
	host, err := s.ctr.Host(ctx)
	if err != nil {
		_ = s.TerminateSilently(ctx)
		return fmt.Errorf("get host: %w", err)
	}
	mapped, err := s.ctr.MappedPort(ctx, nat.Port(s.opts.Port))
	if err != nil {
		_ = s.TerminateSilently(ctx)
		return fmt.Errorf("get mapped port: %w", err)
	}
	base, err := url.Parse(fmt.Sprintf("http://%s:%s", host, mapped.Port()))
	if err != nil {
		_ = s.TerminateSilently(ctx)
		return fmt.Errorf("build base url: %w", err)
	}
	s.baseURL = base

	// Start background collection
	colCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.collecting = true
	s.wg.Add(1)
	go s.collectLoop(colCtx)

	return nil
}

// collectLoop runs until cancel() is invoked. It uses a placeholder poller.
func (s *BackgroundGameServer) collectLoop(ctx context.Context) {
	defer s.wg.Done()

	t := time.NewTicker(s.opts.PollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			raw, err := s.pollOnce(ctx) // Placeholder implementation
			d := time.Since(start)

			sample := MetricSample{
				At:       time.Now(),
				Raw:      raw,
				Err:      err,
				Duration: d,
			}

			s.mu.Lock()
			s.history = append(s.history, sample)
			if s.opts.HistoryLimit > 0 && len(s.history) > s.opts.HistoryLimit {
				// keep last HistoryLimit items
				s.history = s.history[len(s.history)-s.opts.HistoryLimit:]
			}
			s.mu.Unlock()
		}
	}
}

// pollOnce is a placeholder for your real metrics scraping.
// Swap this out for an actual HTTP GET to s.baseURL + s.opts.MetricsPath
// and parse/serialize as you wish.
func (s *BackgroundGameServer) pollOnce(ctx context.Context) (string, error) {
	// Example placeholder that synthesizes a fake metric
	// In a real impl: http.NewRequestWithContext(ctx, "GET", s.baseURL.ResolveReference(...).String(), nil)
	// then read body, maybe parse Prometheus text, JSON, etc.
	fake := fmt.Sprintf("uptime_seconds %d", time.Now().Unix()%3600)
	return fake, nil
}

// History returns a copy of the current in-memory metrics history.
func (s *BackgroundGameServer) History() []MetricSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MetricSample, len(s.history))
	copy(out, s.history)
	return out
}

// Shutdown stops the metrics collector first, then terminates the container.
func (s *BackgroundGameServer) Shutdown(ctx context.Context) error {
	// 1) Stop collecting
	if s.cancel != nil && s.collecting {
		s.cancel()
		s.wg.Wait()
		s.collecting = false
	}

	// 2) Now terminate container
	if s.ctr != nil {
		if err := s.ctr.Terminate(ctx); err != nil {
			return fmt.Errorf("terminate container: %w", err)
		}
	}
	return nil
}

// TerminateSilently helps internal error paths to try to clean up without masking errors.
func (s *BackgroundGameServer) TerminateSilently(ctx context.Context) error {
	defer func() { recover() }() // best-effort
	_ = s.Shutdown(ctx)
	return nil
}

// BaseURL returns the discovered host base URL for convenience (e.g. for client tests).
func (s *BackgroundGameServer) BaseURL() *url.URL {
	return s.baseURL
}
