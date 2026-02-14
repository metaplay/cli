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

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// GameServerOptions configures the server container and the poller behavior.
type GameServerOptions struct {
	Image         string        // e.g. "lovely-wombats-build/server:test"
	SystemPort    string        // container port for SystemHttpServer (usually "8888/tcp")
	PollInterval  time.Duration // how often to collect metrics
	HistoryLimit  int           // max samples kept in memory (0 or <0 => unbounded)
	Env           map[string]string
	ExposedPorts  []string // optional override; defaults to []string{Port}
	ContainerName string   // optional; useful in CI logs
	Cmd           []string // optional command/args to run inside the container (e.g. ["gameserver", "-LogLevel=Information"])
	ExtraArgs     []string // additional args to append to the default Cmd
	ExtraEnv      map[string]string // additional env vars to merge with defaults (overrides on conflict)
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

// Write implements io.Writer so we can io.Copy logs through the consumer directly.
func (c *containerLogConsumer) Write(p []byte) (int, error) {
	if c == nil {
		return len(p), nil
	}
	c.Accept(tc.Log{Content: append([]byte(nil), p...)})
	return len(p), nil
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

// NewGameServer creates a wrapper with the given options (does not start the container).
func NewGameServer(opts GameServerOptions) *BackgroundGameServer {
	// Hard-code all configuration - these are the standard integration test defaults
	opts.SystemPort = "8888/tcp"
	opts.ExposedPorts = []string{"8585/tcp", "8888/tcp", "9090/tcp", "5550/tcp", "5560/tcp"}
	opts.PollInterval = 2 * time.Second
	opts.HistoryLimit = 10

	// Build default env and merge any extra env vars (extra overrides on conflict)
	defaultEnv := map[string]string{
		"ASPNETCORE_ENVIRONMENT":      "Development",
		"METAPLAY_ENVIRONMENT_FAMILY": "Local",
	}
	for k, v := range opts.ExtraEnv {
		defaultEnv[k] = v
	}
	opts.Env = defaultEnv

	// Build default cmd and append any extra args
	defaultCmd := []string{
		"gameserver",
		"-LogLevel=Information",
		// METAPLAY_OPTS (shared with BotClient)
		"--Environment:EnableKeyboardInput=false",
		"--Environment:ExitOnLogError=true",
		// METAPLAY_SERVER_OPTS (server-specific)
		"--Environment:EnableSystemHttpServer=true",
		"--Environment:SystemHttpListenHost=0.0.0.0",
		"--Environment:WaitForSigtermBeforeExit=true",
		"--AdminApi:WebRootPath=wwwroot",
		"--Database:Backend=Sqlite",
		"--Database:SqliteInMemory=true",
		"--Player:ForceFullDebugConfigForBots=false",
	}
	opts.Cmd = append(defaultCmd, opts.ExtraArgs...)

	return &BackgroundGameServer{opts: opts}
}

// Start launches the server container, waits for readiness, and starts metrics collection.
func (s *BackgroundGameServer) Start(ctx context.Context) error {
	// Build port bindings: bind each exposed container port to 127.0.0.1 with a random host port.
	portBindings := nat.PortMap{}
	for _, p := range s.opts.ExposedPorts {
		port := nat.Port(p)
		portBindings[port] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}
	}

	// Build container request
	req := tc.ContainerRequest{
		Image:        s.opts.Image,
		Name:         s.opts.ContainerName,
		ExposedPorts: s.opts.ExposedPorts,
		Env:          s.opts.Env,
		Cmd:          s.opts.Cmd,
		WaitingFor: wait.ForHTTP("/isReady").
			WithPort(nat.Port(s.opts.SystemPort)).
			WithStatusCodeMatcher(func(code int) bool { return code == 200 }).
			WithStartupTimeout(2 * time.Minute),
	}

	// Bind ports on 127.0.0.1 with random host ports via HostConfigModifier
	req.HostConfigModifier = func(hc *dockercontainer.HostConfig) {
		if hc.PortBindings == nil {
			hc.PortBindings = nat.PortMap{}
		}
		for port, bindings := range portBindings {
			hc.PortBindings[port] = bindings
		}
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

	// Start the container first to avoid attach races with Docker
	log.Debug().Msg("Start container...")
	if err := s.container.Start(ctx); err != nil {
		// Best-effort: container failed to start; drain logs for post-mortem before cleanup
		// Attach a temporary consumer to drain logs just for post-mortem
		tmpConsumer := &containerLogConsumer{writer: os.Stdout, prefix: "[server] "}
		_ = s.drainAllLogs(context.Background(), tmpConsumer)
		// Now clean up
		_ = s.Shutdown(context.Background())
		return fmt.Errorf("start container: %w", err)
	}

	// Attach live log consumer AFTER successful start
	// Use a long-lived context so streaming continues past Start(ctx).
	producerCtx, producerCancel := context.WithCancel(context.Background())
	consumer := &containerLogConsumer{writer: os.Stdout, prefix: "[server] "}
	s.container.FollowOutput(consumer)
	if err := s.container.StartLogProducer(producerCtx); err != nil {
		log.Debug().Msgf("Failed to start log producer: %v", err)
	}
	// Store cancel to stop producer (and any background loops sharing the ctx) in Shutdown
	s.cancel = producerCancel
	s.collecting = true

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
			raw, err := s.pollMetricsOnce(ctx) // Placeholder implementation
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

// pollMetricsOnce is a placeholder for your real metrics scraping.
// \todo implement properly
func (s *BackgroundGameServer) pollMetricsOnce(ctx context.Context) (string, error) {
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
	// Stop log producer first (best-effort), then cancel shared ctx users
	if s.container != nil {
		// StopLogProducer is idempotent and safe even if producer wasn't started
		_ = s.container.StopLogProducer()
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

// drainAllLogs fetches the full log buffer once (non-follow) and routes it via the given consumer.
func (s *BackgroundGameServer) drainAllLogs(ctx context.Context, consumer *containerLogConsumer) error {
	if s.container == nil {
		return nil
	}
	r, err := s.container.Logs(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	// Pipe logs to the same consumer using io.Copy for simplicity and efficiency.
	_, _ = io.Copy(consumer, r)
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

// ContainerName returns the container name for network sharing purposes.
func (s *BackgroundGameServer) ContainerName() string {
	return s.opts.ContainerName
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
