package testutil

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
	tc "github.com/testcontainers/testcontainers-go"
)

// RunOnceContainerOptions configures a container that runs to completion.
type RunOnceContainerOptions struct {
	Image         string            // e.g. "myorg/myapp:latest"
	Cmd           []string          // command/args to run inside the container
	Env           map[string]string // environment variables
	ExposedPorts  []string          // optional ports to expose (e.g. ["8080/tcp"])
	ContainerName string            // optional; useful in CI logs
	LogPrefix     string            // prefix for container logs (e.g. "[build] ")
	WorkingDir    string            // optional working directory inside container
	Mounts        []string          // optional bind mounts in "host:container" format
	AutoRemove    bool              // equivalent to docker run --rm (default: true)
}

// RunOnceContainer wraps a container that runs to completion.
type RunOnceContainer struct {
	opts      RunOnceContainerOptions
	container tc.Container
	exitCode  int
	completed bool
}

// NewRunOnce creates a wrapper for a run-once container with the given options.
func NewRunOnce(opts RunOnceContainerOptions) *RunOnceContainer {
	// Set defaults
	if opts.LogPrefix == "" {
		opts.LogPrefix = "[container] "
	}
	if opts.AutoRemove == false && opts.ContainerName == "" {
		// Default to auto-remove if no explicit name is set
		opts.AutoRemove = true
	}
	return &RunOnceContainer{opts: opts}
}

// Run starts the container, streams logs, waits for completion, and returns the exit code.
func (r *RunOnceContainer) Run(ctx context.Context) (int, error) {
	// Build port bindings if any ports are exposed
	portBindings := nat.PortMap{}
	for _, p := range r.opts.ExposedPorts {
		port := nat.Port(p)
		portBindings[port] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}
	}

	// Build container request
	req := tc.ContainerRequest{
		Image:        r.opts.Image,
		Name:         r.opts.ContainerName,
		Cmd:          r.opts.Cmd,
		Env:          r.opts.Env,
		ExposedPorts: r.opts.ExposedPorts,
		WorkingDir:   r.opts.WorkingDir,
		AutoRemove:   r.opts.AutoRemove,
	}

	// Add port bindings if any
	if len(portBindings) > 0 {
		req.HostConfigModifier = func(hc *dockercontainer.HostConfig) {
			if hc.PortBindings == nil {
				hc.PortBindings = nat.PortMap{}
			}
			for port, bindings := range portBindings {
				hc.PortBindings[port] = bindings
			}
		}
	}

	// Add bind mounts if any
	if len(r.opts.Mounts) > 0 {
		binds := make([]string, len(r.opts.Mounts))
		copy(binds, r.opts.Mounts)
		if req.HostConfigModifier == nil {
			req.HostConfigModifier = func(hc *dockercontainer.HostConfig) {
				hc.Binds = binds
			}
		} else {
			originalModifier := req.HostConfigModifier
			req.HostConfigModifier = func(hc *dockercontainer.HostConfig) {
				originalModifier(hc)
				hc.Binds = binds
			}
		}
	}

	log.Debug().Msgf("Create run-once container: name=%s image=%s cmd=%v", r.opts.ContainerName, r.opts.Image, r.opts.Cmd)

	// Create container (not started)
	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil && r.opts.ContainerName != "" && strings.Contains(err.Error(), "is already in use") {
		// Remove existing container with the same name to avoid conflicts
		log.Debug().Msgf("Container name conflict detected; removing existing container name=%s", r.opts.ContainerName)
		if rmErr := removeDockerContainerByName(ctx, r.opts.ContainerName); rmErr != nil {
			log.Debug().Msgf("Failed to remove existing container '%s': %v", r.opts.ContainerName, rmErr)
		}
		// Retry with the original requested name
		ctr, err = tc.GenericContainer(ctx, tc.GenericContainerRequest{
			ContainerRequest: req,
			Started:          false,
		})
	}
	if err != nil {
		return -1, fmt.Errorf("failed to create container: %w", err)
	}
	r.container = ctr

	// Start the container
	log.Debug().Msg("Starting run-once container...")
	if err := r.container.Start(ctx); err != nil {
		// Best-effort: drain logs for post-mortem before cleanup
		tmpConsumer := &containerLogConsumer{writer: os.Stdout, prefix: r.opts.LogPrefix}
		_ = r.drainAllLogs(context.Background(), tmpConsumer)
		// Clean up
		_ = r.cleanup(context.Background())
		return -1, fmt.Errorf("failed to start container: %w", err)
	}

	// Attach log consumer after successful start
	consumer := &containerLogConsumer{writer: os.Stdout, prefix: r.opts.LogPrefix}
	r.container.FollowOutput(consumer)
	if err := r.container.StartLogProducer(ctx); err != nil {
		log.Debug().Msgf("Failed to start log producer: %v", err)
	}

	log.Debug().Msg("Container started; waiting for completion...")

	// Wait for container to complete
	state, err := r.container.State(ctx)
	if err != nil {
		// Try to get logs even if state check failed
		_ = r.drainAllLogs(context.Background(), consumer)
		_ = r.cleanup(context.Background())
		return -1, fmt.Errorf("failed to get container state: %w", err)
	}

	// Poll until container exits
	for state.Running {
		select {
		case <-ctx.Done():
			_ = r.cleanup(context.Background())
			return -1, ctx.Err()
		default:
			// Brief sleep before checking again
			select {
			case <-ctx.Done():
				_ = r.cleanup(context.Background())
				return -1, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			state, err = r.container.State(ctx)
			if err != nil {
				_ = r.drainAllLogs(context.Background(), consumer)
				_ = r.cleanup(context.Background())
				return -1, fmt.Errorf("failed to get container state: %w", err)
			}
		}
	}

	exitCode := int(state.ExitCode)

	r.exitCode = int(exitCode)
	r.completed = true

	log.Debug().Msgf("Container completed with exit code: %d", r.exitCode)

	// Stop log producer
	_ = r.container.StopLogProducer()

	// Clean up if auto-remove is disabled and we need manual cleanup
	if !r.opts.AutoRemove {
		_ = r.cleanup(context.Background())
	}

	return r.exitCode, nil
}

// ExitCode returns the exit code of the completed container.
// Returns -1 if the container hasn't completed yet.
func (r *RunOnceContainer) ExitCode() int {
	if !r.completed {
		return -1
	}
	return r.exitCode
}

// IsCompleted returns true if the container has finished running.
func (r *RunOnceContainer) IsCompleted() bool {
	return r.completed
}

// Cleanup terminates and removes the container if it's still running.
func (r *RunOnceContainer) Cleanup(ctx context.Context) error {
	return r.cleanup(ctx)
}

// cleanup is the internal cleanup method.
func (r *RunOnceContainer) cleanup(ctx context.Context) error {
	if r.container == nil {
		return nil
	}

	// Stop log producer first (best-effort)
	_ = r.container.StopLogProducer()

	// Terminate container
	log.Debug().Msg("Terminating run-once container")
	if err := r.container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate container: %w", err)
	}
	log.Debug().Msg("Run-once container terminated")

	return nil
}

// drainAllLogs fetches the full log buffer once (non-follow) and routes it via the given consumer.
func (r *RunOnceContainer) drainAllLogs(ctx context.Context, consumer *containerLogConsumer) error {
	if r.container == nil {
		return nil
	}
	logReader, err := r.container.Logs(ctx)
	if err != nil {
		return err
	}
	defer logReader.Close()
	// Pipe logs to the consumer using io.Copy for simplicity and efficiency.
	_, _ = io.Copy(consumer, logReader)
	return nil
}

// RunOnceContainerResult holds the result of a completed run-once container.
type RunOnceContainerResult struct {
	ExitCode int
	Error    error
}

// RunContainerToCompletion is a convenience function that creates and runs a container to completion.
// It returns the exit code and any error that occurred.
func RunContainerToCompletion(ctx context.Context, opts RunOnceContainerOptions) (int, error) {
	container := NewRunOnce(opts)
	return container.Run(ctx)
}
