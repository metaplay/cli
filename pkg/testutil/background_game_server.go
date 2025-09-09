package testutil

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// GameServerOptions configures the server container and the poller behavior.
type GameServerOptions struct {
	Image         string        // e.g. "myorg/myserver:latest"
	SystemPort    string        // container port for SystemHttpServer (usually "8888/tcp")
	MetricsPath   string        // e.g. "/metrics"
	PollInterval  time.Duration // how often to collect metrics
	HistoryLimit  int           // max samples kept in memory (0 or <0 => unbounded)
	Env           map[string]string
	ExposedPorts  []string // optional override; defaults to []string{Port}
	ContainerName string   // optional; useful in CI logs
}

// containerLogConsumer mirrors container logs to an io.Writer (e.g. os.Stdout).
type containerLogConsumer struct {
	prefix string
	writer io.Writer
}

// Accept implements testcontainers-go LogConsumer interface.
func (c *containerLogConsumer) Accept(l tc.Log) {
	if c == nil || c.writer == nil {
		return
	}
	_, _ = c.writer.Write([]byte(c.prefix + string(l.Content)))
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

	container  tc.Container
	baseURL    *url.URL
	mu         sync.RWMutex
	history    []MetricSample
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	collecting bool
}

// New creates a wrapper with the given options (does not start the container).
func New(opts GameServerOptions) *BackgroundGameServer {
	if len(opts.ExposedPorts) == 0 && opts.SystemPort != "" {
		opts.ExposedPorts = []string{opts.SystemPort}
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
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort(nat.Port(s.opts.SystemPort)).
			WithStatusCodeMatcher(func(code int) bool { return code == 200 }).
			WithStartupTimeout(2 * time.Minute),
	}

	log.Debug().Msgf("Create container: name=%s image=%s ports=%v", s.opts.ContainerName, s.opts.Image, s.opts.ExposedPorts)

	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false, // create but do not start yet to attach logs first
	})
	if err != nil && s.opts.ContainerName != "" && strings.Contains(err.Error(), "is already in use") {
		// Remove existing container with the same name to avoid leaks, then retry with the original name.
		log.Debug().Msgf("Container name conflict detected; removing existing container name=%s", s.opts.ContainerName)
		if rmErr := removeDockerContainerByName(ctx, s.opts.ContainerName); rmErr != nil {
			log.Debug().Msgf("Failed to remove existing container '%s': %v", s.opts.ContainerName, rmErr)
		}
		// Retry with the original requested name
		ctr, err = tc.GenericContainer(ctx, tc.GenericContainerRequest{
			ContainerRequest: req,
			Started:          false,
		})
	}
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	s.container = ctr

	// Attach live log consumer BEFORE starting
	// Use a long-lived context for the log producer so it keeps streaming even if Start(ctx) fails or times out.
	producerCtx, producerCancel := context.WithCancel(context.Background())
	consumer := &containerLogConsumer{writer: os.Stdout, prefix: "[server] "}
	s.container.FollowOutput(consumer)
	if err := s.container.StartLogProducer(producerCtx); err != nil {
		log.Debug().Msgf("Failed to start log producer: %v", err)
	}
	// Store cancel to stop producer (and any background loops sharing the ctx) in Shutdown
	s.cancel = producerCancel
	s.collecting = true

	// Now start the container (logs will flow immediately)
	log.Debug().Msg("Start container...")
	if err := s.container.Start(ctx); err != nil {
		// Best-effort: container failed to start; drain logs for post-mortem before cleanup
		_ = s.drainAllLogs(context.Background(), os.Stdout)
		// Now clean up
		_ = s.Shutdown(context.Background())
		return fmt.Errorf("start container: %w", err)
	}

	log.Debug().Msg("Container started; resolving host and mapped port")

	// Discover host:port where we can reach the server from the host
	if portMap, perr := s.container.Ports(ctx); perr == nil {
		log.Debug().Msgf("Container exposed ports: %v", portMap)
	}
	host, err := s.container.Host(ctx)
	if err != nil {
		_ = s.TerminateSilently(ctx)
		return fmt.Errorf("get host: %w", err)
	}
	mapped, err := s.container.MappedPort(ctx, nat.Port(s.opts.SystemPort))
	if err != nil {
		_ = s.TerminateSilently(ctx)
		if portMap, perr := s.container.Ports(ctx); perr == nil {
			return fmt.Errorf("get mapped port: %w; available ports: %v", err, portMap)
		}
		return fmt.Errorf("get mapped port: %w", err)
	}
	base, err := url.Parse(fmt.Sprintf("http://%s:%s", host, mapped.Port()))
	if err != nil {
		_ = s.TerminateSilently(ctx)
		return fmt.Errorf("build base url: %w", err)
	}
	s.baseURL = base
	log.Debug().Msgf("Resolved server base URL: %s", base.String())

	// Start background metrics collection using the same long-lived context
	log.Debug().Msg("Starting background metrics collection loop")
	s.wg.Go(func() { s.collectLoop(producerCtx) })

	return nil
}

// collectLoop runs until cancel() is invoked. It uses a placeholder poller.
func (s *BackgroundGameServer) collectLoop(ctx context.Context) {
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
	log.Debug().Msg("Shutting down BackgroundGameServer")
	// Mark collecting false first
	if s.collecting {
		s.collecting = false
	}
	// Stop producer and any background loops sharing the same cancel
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	// Wait for background goroutines to finish
	s.wg.Wait()

	// Terminate container last
	if s.container != nil {
		log.Debug().Msg("Terminating container")
		if err := s.container.Terminate(ctx); err != nil {
			return fmt.Errorf("terminate container: %w", err)
		}
		log.Debug().Msg("Container terminated")
	}
	return nil
}

// drainAllLogs fetches the full log buffer once (non-follow) and writes it to w.
func (s *BackgroundGameServer) drainAllLogs(ctx context.Context, w io.Writer) error {
	if s.container == nil {
		return nil
	}
	r, err := s.container.Logs(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	_, _ = io.Copy(w, r)
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

// removeDockerContainerByName force removes a container by name using the local docker CLI.
// Best-effort: if removal fails, the error is returned but the caller may choose to proceed.
func removeDockerContainerByName(ctx context.Context, name string) error {
	// docker rm -f <name>
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm -f %s failed: %v, output: %s", name, err, string(output))
	}
	return nil
}
