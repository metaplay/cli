/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package interrupt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const forceQuitChildEnv = "METAPLAY_INTERRUPT_FORCE_QUIT_CHILD"

// forceQuitMark prefixes the line on which the child reports the time of its second SIGINT.
const forceQuitMark = "second SIGINT at "

// testSIGINTIgnoredAtStart is true when the test binary started with SIGINT ignored, which the
// child inherits. It is read before any test calls signal.Notify.
var testSIGINTIgnoredAtStart = signal.Ignored(syscall.SIGINT)

// A force-quit ends the process as the platform's scripts expect: by the signal on Unix, so a
// shell sees an interrupted process, and with ExitCode on Windows, where a Ctrl-C cannot be sent
// to the process itself, so the child calls the exit directly.
func TestForceQuitEndsProcess(t *testing.T) {
	if os.Getenv(forceQuitChildEnv) == "1" {
		runForceQuitChild(t)
		return
	}

	exitErr, output, _ := runForceQuitParent(t, exec.Command(os.Args[0], "-test.run=^TestForceQuitEndsProcess$"))
	if runtime.GOOS == "windows" || testSIGINTIgnoredAtStart {
		// The child cannot end by SIGINT, see TestForceQuitWithSIGINTIgnoredAtStart
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
	exitErr, output, elapsed := runForceQuitParent(t, shell)
	if code := exitErr.ExitCode(); code != ExitCode {
		t.Errorf("exit code = %d, want %d\n%s", code, ExitCode, output)
	}
	// Waiting for the re-raised signal would take the 1 s fallback in exitOnSignal
	if elapsed == 0 {
		t.Fatalf("the child reported no second SIGINT\n%s", output)
	}
	if elapsed >= 900*time.Millisecond {
		t.Errorf("child took %v from the second SIGINT to its exit, want under 900ms", elapsed)
	}
}

// runForceQuitParent runs cmd as the child of TestForceQuitEndsProcess. It returns how the child
// failed, its output, and the time from its second SIGINT to its exit, which leaves out the time to
// start the child.
func runForceQuitParent(t *testing.T, cmd *exec.Cmd) (*exec.ExitError, []byte, time.Duration) {
	t.Helper()
	cmd.Env = append(os.Environ(), forceQuitChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	exited := time.Now()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("child did not fail: %v\n%s", err, output)
	}
	var elapsed time.Duration
	for line := range strings.Lines(string(output)) {
		if after, found := strings.CutPrefix(line, forceQuitMark); found {
			nanos, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64)
			if err != nil {
				t.Fatalf("bad line from the child: %q", line)
			}
			elapsed = exited.Sub(time.Unix(0, nanos))
		}
	}
	return exitErr, output, elapsed
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
	fmt.Printf("%s%d\n", forceQuitMark, time.Now().UnixNano())
	sendSIGINT(t)
	time.Sleep(3 * time.Second)
	os.Exit(0) // Not force-quit
}
