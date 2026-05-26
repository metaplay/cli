/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/go-version"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// SignaledError indicates a child process exited after a forwarded
// SIGINT/SIGTERM (or was killed by an uncaught signal). It renders
// identically to the underlying error — callers that want to tolerate
// Ctrl+C detect it with errors.As.
type SignaledError struct {
	Signal os.Signal
	Err    error
}

func (e *SignaledError) Error() string { return e.Err.Error() }
func (e *SignaledError) Unwrap() error { return e.Err }

// isCmdShim reports whether cmd.Path resolves to a Windows .cmd or .bat
// script. exec.Cmd invokes those via cmd.exe, which shows
// "Terminate batch job (Y/N)?" on Ctrl+C and blocks until answered —
// callers use this to opt into a faster Cancel policy (kill the whole
// process tree atomically) instead of giving the child a graceful window.
func isCmdShim(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".cmd") || strings.HasSuffix(p, ".bat")
}

// applyCancelPolicy configures cmd.Cancel and cmd.WaitDelay for graceful
// vs. fast shutdown when ctx is canceled. It returns a setter the caller
// must invoke after startCmd to wire the Job-Object cleanup into Cancel.
//
//   - .cmd/.bat shims (pnpm.cmd, playwright.cmd …): close the Job Object
//     immediately on cancel. Otherwise cmd.exe would prompt
//     "Terminate batch job (Y/N)?" and block until WaitDelay force-kills
//     it. WaitDelay stays as a small safety net.
//   - native executables (dotnet, docker, …): no-op Cancel + 10 s
//     WaitDelay. The child handles its own SIGINT/CTRL_C_EVENT
//     (forwarding to containers, flushing build state, etc.); we only
//     force-kill if it wedges.
func applyCancelPolicy(cmd *exec.Cmd) func(cleanup func()) {
	var (
		mu      sync.Mutex
		cleanup func()
	)
	if isCmdShim(cmd.Path) {
		cmd.Cancel = func() error {
			mu.Lock()
			c := cleanup
			mu.Unlock()
			if c != nil {
				c()
			}
			return nil
		}
		cmd.WaitDelay = 2 * time.Second
	} else {
		cmd.Cancel = func() error { return nil }
		cmd.WaitDelay = 10 * time.Second
	}
	return func(c func()) {
		mu.Lock()
		cleanup = c
		mu.Unlock()
	}
}

// Environment variables to pass to all dotnet commands.
var commonDotnetEnvVars = []string{
	"DOTNET_NOLOGO=1",                                // Hide the welcome/telemetry banner
	"DOTNET_SKIP_WORKLOAD_INTEGRITY_CHECK=1",         // Skip the first-run workload integrity check
	"DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE=true", // Don't notify about updates to workloads
}

// Provide installation instructions based on the operating system
func getDotnetInstallInstructions() string {
	switch runtime.GOOS {
	case "windows":
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
1. Go to: https://dotnet.microsoft.com/download
2. Download and run the installer for desired .NET SDK version.
3. Follow the installation steps and ensure 'dotnet' is added to your PATH.
`
	case "darwin":
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
1. Open a terminal.
2. Install Homebrew (if not installed): https://brew.sh/
3. Run: brew install --cask dotnet-sdk
4. Add .NET SDK to your PATH by running: export PATH="$PATH:/usr/local/share/dotnet"
5. Verify installation with: dotnet --version
`
	case "linux":
		return `.NET SDK is missing or outdated.
Please install or upgrade .NET SDK: https://learn.microsoft.com/en-us/dotnet/core/install/linux`
	default:
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
Visit: https://dotnet.microsoft.com/download for instructions specific to your operating system.
`
	}
}

// Checks if .NET SDK is installed and check that it is recent enough for the SDK
// version used.
func checkDotnetSdkVersion(ctx context.Context, requiredDotnetVersion *version.Version) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Note: This gets the SDK version, not runtime version (eg, 8.0.400)
	cmd := exec.CommandContext(ctx, "dotnet", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return clierrors.New(".NET SDK is not installed or not in PATH").
			WithSuggestion(getDotnetInstallInstructions())
	}

	// Parse installed .NET version
	installedVersionStr := strings.TrimSpace(out.String())
	installedVersion, err := version.NewVersion(installedVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse required .NET version string '%s': %v", installedVersionStr, err)
	}

	// Print the info.
	badge := styles.RenderMuted(fmt.Sprintf("[minimum: %s]", requiredDotnetVersion))
	log.Info().Msgf("%s .NET SDK detected: %s %s", styles.RenderSuccess("✓"), styles.RenderTechnical(installedVersion.String()), badge)

	// Check that .NET version is recent enough
	if installedVersion.LessThan(requiredDotnetVersion) {
		return clierrors.Newf(".NET SDK version %s or higher is required, but found %s", requiredDotnetVersion, installedVersion).
			WithSuggestion(getDotnetInstallInstructions())
	}

	log.Info().Msg("")
	return nil
}

func execChildTask(ctx context.Context, workingDir string, binary string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setCleanup := applyCancelPolicy(cmd)

	log.Info().Msg(styles.RenderMuted(fmt.Sprintf("%s$ %s %s", workingDir, binary, strings.Join(args, " "))))
	cleanup, err := startCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", binary, err)
	}
	setCleanup(cleanup)
	defer cleanup()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s exited with error: %w", binary, err)
	}

	return nil
}

// Runs a child process in "interactive" mode where all inputs/outputs are forwarded
// to the sub-process. If extraEnv is specified, its contents are appended to the current
// environment variables.
//
// If ctx is already canceled, returns ctx.Err() without spawning anything — this
// matters when a previous Ctrl+C canceled the root context: we don't want to
// start the next pipeline step (e.g. `pnpm install` after a Ctrl+C during the
// preceding version check).
func execChildInteractive(ctx context.Context, workingDir string, binary string, args []string, extraEnv []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Create the command to run the .NET binary
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// If extraEnv is given, append it to the current process's env variables.
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	// We forward signals to the child manually (see goroutine below) and
	// also rely on OS-level signal delivery (process group on Unix, console
	// attachment on Windows). exec.CommandContext's default Cancel is
	// Process.Kill — that would race ahead of our graceful signal and
	// SIGKILL/TerminateProcess the child mid-shutdown. applyCancelPolicy
	// installs a no-op (or, for .cmd/.bat shims on Windows, a Job-Object-
	// closing) Cancel along with a WaitDelay safety net.
	setCleanup := applyCancelPolicy(cmd)

	// Create a channel to forward signals to the subprocess. Track whether
	// we forwarded one so we can mark the resulting error as signal-induced
	// — needed on Windows in particular, where TerminateProcess just sets
	// an exit code and ProcessState can't distinguish signal-kill from a
	// normal failure.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	var (
		mu           sync.Mutex
		forwardedSig os.Signal
	)

	defer func() {
		signal.Stop(signalChan)
		close(signalChan)
	}()

	// Start the child. On Windows this attaches it to a Job Object so the
	// entire process tree dies together when we exit — without that, the
	// .cmd shims that fork pnpm/npm leave node descendants alive on Ctrl+C.
	// startCmd is a thin wrapper over cmd.Start on other platforms.
	cleanup, err := startCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", binary, err)
	}
	setCleanup(cleanup)
	defer cleanup()

	// Goroutine to forward signals to the subprocess. Exits when signalChan is closed.
	go func() {
		for sig := range signalChan {
			mu.Lock()
			forwardedSig = sig
			mu.Unlock()
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	// Wait for the subprocess to complete
	err = cmd.Wait()
	if err == nil {
		return nil
	}

	wrapped := fmt.Errorf("binary exited with error: %w", err)

	mu.Lock()
	sig := forwardedSig
	mu.Unlock()

	// On Unix, syscall.WaitStatus.Signaled() reports signal-induced exit
	// reliably (unlike ExitCode == -1, which also fires for processes that
	// haven't exited). Windows can't distinguish via ProcessState, so we
	// fall back to whether we forwarded a signal ourselves.
	killedBySignal := sig != nil || wasKilledBySignal(cmd.ProcessState)

	if killedBySignal {
		return &SignaledError{Signal: sig, Err: wrapped}
	}
	return wrapped
}
