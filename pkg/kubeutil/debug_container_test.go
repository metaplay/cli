/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/remotecommand"
)

// ShellQuote's output, run through bash or sh, is the original string, so 'rm -f' gets exactly one
// argument even for names with spaces, wildcards or quotes.
func TestShellQuoteRoundTripsThroughShells(t *testing.T) {
	names := []string{"/tmp/dump.gcdump", "/tmp/*", "/tmp/heap dump.gcdump", "/tmp/it's", "/tmp/$(id)", "-rf", `pa$$w'o"rd!`}
	for _, shellName := range []string{"bash", "sh"} {
		shell, err := exec.LookPath(shellName)
		if err != nil {
			t.Logf("%s not found", shellName)
			continue
		}
		for _, name := range names {
			out, err := exec.Command(shell, "-c", "printf '%s\\n' "+ShellQuote(name)).Output()
			if err != nil {
				t.Fatalf("%s failed for %q: %v", shellName, name, err)
			}
			if got := strings.TrimSuffix(string(out), "\n"); got != name {
				t.Errorf("ShellQuote(%q) through %s = %q", name, shellName, got)
			}
		}
	}
}

// Before removing a file, the command stops a cancelled download that still reads it, without
// matching its own shell or a file whose name only starts the same.
func TestRemoveFilesCommand(t *testing.T) {
	got := removeFilesCommand([]string{"/tmp/a.gcdump", "/tmp/it's"})
	want := `for pid in $(pgrep -f -- '[d]d if=/tmp/a\.gcdump '); do pkill -P $pid; kill $pid; done; ` +
		`for pid in $(pgrep -f -- '[d]d if=/tmp/it'\''s '); do pkill -P $pid; kill $pid; done; ` +
		`rm -f -- '/tmp/a.gcdump' '/tmp/it'\''s'`
	if got != want {
		t.Errorf("removeFilesCommand =\n%s\nwant\n%s", got, want)
	}
}

// The continued context outlives the parent's cancellation by the grace period, and no longer.
func TestContinueAfterCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancelParent := context.WithCancel(context.Background())
		continued, cancel := continueAfterCancel(parent, time.Minute)
		defer cancel()

		cancelParent()
		time.Sleep(59 * time.Second)
		if err := continued.Err(); err != nil {
			t.Fatalf("continued context cancelled before the grace period ended: %v", err)
		}
		time.Sleep(2 * time.Second)
		if continued.Err() == nil {
			t.Fatal("continued context not cancelled after the grace period")
		}
	})
}

// Without a cancellation of the parent, only the returned cancel function ends the context.
func TestContinueAfterCancelWithoutParentCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		continued, cancel := continueAfterCancel(context.Background(), time.Second)
		time.Sleep(time.Minute)
		if err := continued.Err(); err != nil {
			t.Fatalf("continued context cancelled without a parent cancellation: %v", err)
		}
		cancel()
		if continued.Err() == nil {
			t.Fatal("cancel did not cancel the context")
		}
	})
}

// fakeExecutor is a remote command whose stream ends with err.
type fakeExecutor struct{ err error }

func (f fakeExecutor) Stream(remotecommand.StreamOptions) error { return f.err }

func (f fakeExecutor) StreamWithContext(context.Context, remotecommand.StreamOptions) error {
	return f.err
}

// Once the context is done, a stream's error counts as a cancellation, and the text names the
// cancellation once: also when client-go has flattened it into its own error text.
func TestStreamWithContextWrapsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	failure := errors.New("error sending request")

	if err := StreamWithContext(ctx, fakeExecutor{failure}, remotecommand.StreamOptions{}); err != failure { //nolint:errorlint // The exact error, not a wrapper
		t.Errorf("error before the cancellation = %v, want the stream's error as it is", err)
	}

	cancel()
	err := StreamWithContext(ctx, fakeExecutor{failure}, remotecommand.StreamOptions{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
		t.Errorf("error = %v, want both the cancellation and the stream's error", err)
	}
	if got, want := err.Error(), "context canceled: error sending request"; got != want {
		t.Errorf("error text = %q, want %q", got, want)
	}
	flattened := errors.New(`error sending request: Post "https://host/exec": context canceled`)
	err = StreamWithContext(ctx, fakeExecutor{flattened}, remotecommand.StreamOptions{})
	if !errors.Is(err, context.Canceled) || err.Error() != flattened.Error() {
		t.Errorf("error = %v, want a cancellation with the stream's text as it is", err)
	}
	err = StreamWithContext(ctx, fakeExecutor{context.Canceled}, remotecommand.StreamOptions{})
	if err == nil || err.Error() != "context canceled" {
		t.Errorf("error = %v, want the cancellation once", err)
	}
}

// The idle container's main process records its own PID, the 'sleep' it becomes, and the stop
// command ends it.
func TestIdleContainerCommandStops(t *testing.T) {
	// On Windows, Git's bash can be on PATH, but it takes neither Windows paths nor Windows PIDs.
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	pidFile := t.TempDir() + "/pid"
	command := idleContainerCommand(pidFile, time.Hour)
	main := exec.Command("bash", command[1:]...)
	if err := main.Start(); err != nil {
		t.Fatal(err)
	}
	// The stop command ends it with SIGTERM, which makes Wait return an error.
	ended := make(chan struct{})
	go func() { _ = main.Wait(); close(ended) }()
	defer func() { _ = main.Process.Kill() }()

	// Wait for the PID file.
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(main.Process.Pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PID file = %q, %v; want %d", data, err, main.Process.Pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if out, err := exec.Command("bash", "-c", stopIdleContainerCommand(pidFile)).CombinedOutput(); err != nil {
		t.Fatalf("stop command failed: %v: %s", err, out)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the main process did not end after the stop command")
	}
}

// The trailer of withExitTrailer comes back off the output exactly, with the command's exit code,
// also when the command exits or prints no final newline.
func TestExitTrailerRoundTripsThroughShell(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	cases := []struct {
		command  string
		stdout   string
		exitCode int
	}{
		{"echo hello", "hello\n", 0},
		{"printf 'no newline'", "no newline", 0},
		{"printf ''", "", 0},
		{"echo before; exit 3", "before\n", 3},
		{"true & echo started", "started\n", 0},
		{"echo '__metaplay_exit=9'", "__metaplay_exit=9\n", 0},
	}
	for _, c := range cases {
		out, err := exec.Command(shell, "-c", withExitTrailer(c.command)).Output()
		if err != nil {
			t.Fatalf("%q: %v", c.command, err)
		}
		stdout, exitCode, err := splitExitTrailer(string(out))
		if err != nil || stdout != c.stdout || exitCode != c.exitCode {
			t.Errorf("%q: got %q, %d, %v; want %q, %d", c.command, stdout, exitCode, err, c.stdout, c.exitCode)
		}
	}
}

// Output without the whole trailer was cut short.
func TestSplitExitTrailerRejectsCutOutput(t *testing.T) {
	full := "12345\n" + execExitMarker + "0\n"
	for _, cut := range []string{"", "12345\n", "12345\n\n__metaplay_exit=", "12345\n\n__metaplay_exit=0", full[:len(full)-3]} {
		if _, _, err := splitExitTrailer(cut); !errors.Is(err, errOutputCutShort) {
			t.Errorf("splitExitTrailer(%q) error = %v, want errOutputCutShort", cut, err)
		}
	}
}
