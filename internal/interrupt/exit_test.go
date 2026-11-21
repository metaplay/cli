/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package interrupt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

const forceQuitChildEnv = "METAPLAY_INTERRUPT_FORCE_QUIT_CHILD"

// A force-quit ends the process as the platform's scripts expect: by the signal on Unix, so a
// shell sees an interrupted process, and with ExitCode on Windows, where a Ctrl-C cannot be sent
// to the process itself, so the child calls the exit directly.
func TestForceQuitEndsProcess(t *testing.T) {
	if os.Getenv(forceQuitChildEnv) == "1" {
		runForceQuitChild(t)
		return
	}

	exitErr, output := runForceQuitParent(t, exec.Command(os.Args[0], "-test.run=^TestForceQuitEndsProcess$"))
	if runtime.GOOS == "windows" {
		if code := exitErr.ExitCode(); code != ExitCode {
			t.Errorf("exit code = %d, want %d\n%s", code, ExitCode, output)
		}
		return
	}
	status, _ := exitErr.Sys().(syscall.WaitStatus)
	if !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Errorf("child ended with %v, want killed by SIGINT\n%s", exitErr, output)
	}
}

// A process that started with SIGINT ignored, such as a background job of a script, cannot end by
// re-raising it. It exits with ExitCode at once, without waiting for the re-raised signal.
func TestForceQuitWithSIGINTIgnoredAtStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no inherited signal dispositions on Windows")
	}
	shell := exec.Command("sh", "-c", `trap '' INT; exec "$0" -test.run='^TestForceQuitEndsProcess$'`, os.Args[0])
	start := time.Now()
	exitErr, output := runForceQuitParent(t, shell)
	elapsed := time.Since(start)
	if code := exitErr.ExitCode(); code != ExitCode {
		t.Errorf("exit code = %d, want %d\n%s", code, ExitCode, output)
	}
	// Waiting for the re-raised signal would take the 1 s fallback in exitOnSignal
	if elapsed >= 900*time.Millisecond {
		t.Errorf("child took %v to force-quit, want under 900ms", elapsed)
	}
}

// runForceQuitParent runs cmd as the child of TestForceQuitEndsProcess and returns how it failed.
func runForceQuitParent(t *testing.T, cmd *exec.Cmd) (*exec.ExitError, []byte) {
	t.Helper()
	cmd.Env = append(os.Environ(), forceQuitChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("child did not fail: %v\n%s", err, output)
	}
	return exitErr, output
}

func runForceQuitChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		forceExit(os.Interrupt)
		os.Exit(0)
	}
	ctx, stop := NotifyContext(context.Background(), nil)
	defer stop()
	sendSIGINT(t)
	<-ctx.Done()
	sendSIGINT(t)
	time.Sleep(3 * time.Second)
	os.Exit(0) // Not force-quit
}
