/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/remotecommand"
)

// ShellQuote's output, run through bash, is the original string, so 'rm -f' gets exactly one
// argument even for names with spaces, wildcards or quotes.
func TestShellQuoteRoundTripsThroughBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	names := []string{"/tmp/dump.gcdump", "/tmp/*", "/tmp/heap dump.gcdump", "/tmp/it's", "/tmp/$(id)", "-rf"}
	for _, name := range names {
		out, err := exec.Command(bash, "-c", "printf '%s\\n' "+ShellQuote(name)).Output()
		if err != nil {
			t.Fatalf("bash failed for %q: %v", name, err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != name {
			t.Errorf("ShellQuote(%q) through bash = %q", name, got)
		}
	}
}

// Before removing a file, the command stops a cancelled download that still reads it, without
// matching its own shell or a file whose name only starts the same.
func TestRemoveFilesCommand(t *testing.T) {
	got := removeFilesCommand([]string{"/tmp/a.gcdump", "/tmp/it's"})
	want := `for pid in $(pgrep -f -- '[d]d if=['\'']*/tmp/a\.gcdump['\'']* '); do pkill -P $pid; kill $pid; done; ` +
		`for pid in $(pgrep -f -- '[d]d if=['\'']*/tmp/it'\''s['\'']* '); do pkill -P $pid; kill $pid; done; ` +
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

// The command stops a download, as streamFileFromOffset runs it, of a file whose name has a space:
// both its shell, whose command line has the path in quotes, and its 'dd'.
func TestRemoveFilesCommandStopsDownload(t *testing.T) {
	for _, tool := range []string{"sh", "pgrep", "pkill", "mkfifo", "dd", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	// A FIFO without a writer keeps 'dd' waiting to open it, so the download runs until stopped.
	path := filepath.Join(t.TempDir(), "heap dump.gcdump")
	if err := exec.Command("mkfifo", path).Run(); err != nil {
		t.Fatal(err)
	}
	download := exec.Command("sh", "-c", "dd if="+ShellQuote(path)+" bs=1M skip=0 iflag=skip_bytes 2>/dev/null | gzip -c")
	if err := download.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- download.Wait() }()
	time.Sleep(200 * time.Millisecond) // Let the shell start dd

	if out, err := exec.Command("sh", "-c", removeFilesCommand([]string{path})).CombinedOutput(); err != nil {
		t.Fatalf("remove command failed: %v: %s", err, out)
	}
	select {
	case err := <-done:
		t.Logf("download ended: %v", err) // Killed, so with an error
	case <-time.After(5 * time.Second):
		_ = download.Process.Kill()
		t.Fatal("the download still runs after the remove command")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file not removed: %v", err)
	}
}
