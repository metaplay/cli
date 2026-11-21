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
		runForceQuitChild()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestForceQuitEndsProcess$")
	cmd.Env = append(os.Environ(), forceQuitChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("child did not fail: %v\n%s", err, output)
	}
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

func runForceQuitChild() {
	if runtime.GOOS == "windows" {
		forceExit(os.Interrupt)
		os.Exit(0)
	}
	ctx, stop := NotifyContext(context.Background(), nil)
	defer stop()
	self, _ := os.FindProcess(os.Getpid())
	_ = self.Signal(syscall.SIGINT)
	<-ctx.Done()
	_ = self.Signal(syscall.SIGINT)
	time.Sleep(10 * time.Second)
	os.Exit(0) // Not force-quit
}
