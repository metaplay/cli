//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/metaplay/cli/internal/interrupt"
)

const forceQuitChildEnv = "METAPLAY_CMD_FORCE_QUIT_CHILD"

// A force-quit kills a child process that startCmd started, also one that ignores SIGINT, so it
// does not keep running after the CLI. The CLI runs as a child test process here, since a
// force-quit ends the process.
func TestForceQuitKillsChildProcess(t *testing.T) {
	if pidFile := os.Getenv(forceQuitChildEnv); pidFile != "" {
		runForceQuitCLI(t, pidFile)
		return
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cli := exec.Command(os.Args[0], "-test.run=^TestForceQuitKillsChildProcess$")
	cli.Env = append(os.Environ(), forceQuitChildEnv+"="+pidFile)
	output, err := cli.CombinedOutput()
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("CLI did not force-quit: %v\n%s", err, output)
	}

	content, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the child process wrote no PID: %v\n%s", err, output)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		t.Fatalf("bad PID %q: %v", content, err)
	}
	// The killed child is reaped by init once the CLI has exited
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child process %d still runs after the CLI force-quit", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runForceQuitCLI starts a child process that ignores SIGINT and SIGTERM, then interrupts itself
// twice.
func runForceQuitCLI(t *testing.T, pidFile string) {
	ctx, stop := interrupt.NotifyContext(context.Background(), nil)
	defer stop()

	child := exec.CommandContext(ctx, "sh", "-c", `trap '' INT TERM; echo $$ > "$0.tmp"; mv "$0.tmp" "$0"; while :; do sleep 0.1; done`, pidFile)
	cleanup, err := startCmd(child)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	<-ctx.Done()
	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	time.Sleep(5 * time.Second)
	os.Exit(0) // Not force-quit
}
