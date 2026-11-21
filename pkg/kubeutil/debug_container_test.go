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
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
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

// The command removes each file by its exact name, after stopping the downloads of the files,
// which TestRemoveFilesCommandStopsDownload tests.
func TestRemoveFilesCommand(t *testing.T) {
	got := removeFilesCommand([]string{"/tmp/a.gcdump", "/tmp/it's"})
	if want := `; rm -f -- '/tmp/a.gcdump' '/tmp/it'\''s'`; !strings.HasSuffix(got, want) {
		t.Errorf("removeFilesCommand = %s, want it to end with %s", got, want)
	}
}

// The download pattern matches the command lines of a download's shell and 'dd', and not those of
// another file whose name only starts the same, or of the shell that runs the pattern.
func TestDownloadPattern(t *testing.T) {
	for _, path := range []string{"/tmp/a.gcdump", "/tmp/it's", "/tmp/heap dump+1 (a|b){2}?.gcdump", "/tmp/a''b"} {
		pattern := regexp.MustCompile(downloadPattern(path))
		shellLine := "sh -c " + streamFileCommand(path, 0, true)
		ddLine := "dd if=" + path + " bs=1M skip=0 iflag=skip_bytes"
		for _, line := range []string{shellLine, ddLine} {
			if !pattern.MatchString(line) {
				t.Errorf("pattern %q does not match %q", pattern, line)
			}
		}
		longer := path + " dump.gcdump" // A file whose name starts with path
		quoteAfter := path + "'"        // A path whose plain form looks like path's quoted one
		otherLines := []string{
			"sh -c " + streamFileCommand(longer, 0, true),
			"dd if=" + longer + " bs=1M skip=0 iflag=skip_bytes",
			"dd if=" + quoteAfter + " bs=1M skip=0 iflag=skip_bytes",
			"pgrep -f -- " + ShellQuote(downloadPattern(path)),
		}
		for _, line := range otherLines {
			if pattern.MatchString(line) {
				t.Errorf("pattern %q matches %q", pattern, line)
			}
		}
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
	// The command as the kubelet runs it from the container's spec
	spec := newDebugEphemeralContainer("debugger", "server", false, false, idleContainerCommand(pidFile, time.Hour))
	command := expandLikeKubernetes(spec.Command, spec.Env)
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

// The command stops a download, as streamFileFromOffset runs it, of a file whose name has a space,
// a quote or regular expression operators: both its shell, whose command line has the path quoted,
// and its 'dd'.
func TestRemoveFilesCommandStopsDownload(t *testing.T) {
	requireSkipBytesDD(t)
	for _, tool := range []string{"pgrep", "pkill", "mkfifo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	for _, name := range []string{"heap dump.gcdump", "it's.gcdump", "heap+1 (a|b){2}?.gcdump"} {
		// A FIFO without a writer keeps 'dd' waiting to open it, so the download runs until stopped.
		path := filepath.Join(t.TempDir(), name)
		if err := exec.Command("mkfifo", path).Run(); err != nil {
			t.Fatal(err)
		}
		download := exec.Command("sh", "-c", streamFileCommand(path, 0, true))
		if err := download.Start(); err != nil {
			t.Fatal(err)
		}
		// When the test fails, stop what the remove command did not: dd would wait for a writer
		// to the FIFO forever.
		t.Cleanup(func() {
			_ = exec.Command("pkill", "-KILL", "-P", strconv.Itoa(download.Process.Pid)).Run()
			_ = download.Process.Kill()
		})
		done := make(chan error, 1)
		go func() { done <- download.Wait() }()
		// Wait until dd runs, waiting to open the FIFO
		for deadline := time.Now().Add(5 * time.Second); ; {
			if exec.Command("pgrep", "-f", "--", "^dd if="+regexp.QuoteMeta(path)+" ").Run() == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%q: dd did not start", name)
			}
			time.Sleep(5 * time.Millisecond)
		}

		if out, err := exec.Command("sh", "-c", removeFilesCommand([]string{path})).CombinedOutput(); err != nil {
			t.Fatalf("%q: remove command failed: %v: %s", name, err, out)
		}
		select {
		case err := <-done:
			// Had only 'dd' been stopped, gzip would end, and the shell would exit with gzip's 0.
			// The shell is killed, or exits with the code of its killed gzip.
			if err == nil {
				t.Errorf("%q: the download's shell exited with 0, want it stopped", name)
			}
		case <-time.After(5 * time.Second):
			_ = download.Process.Kill()
			t.Fatalf("%q: the download still runs after the remove command", name)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q: file not removed: %v", name, err)
		}
	}
}

// The commands of the debug container and pod specs reach the container as they were given, after
// the kubelet's expansion of "$(VAR)" and "$$".
func TestDebugSpecCommandsRunAsGiven(t *testing.T) {
	command := []string{"/bin/bash", "-c", "echo $$ > /tmp/pid; echo $(TERM) $HOME $"}
	container := newDebugEphemeralContainer("debugger", "server", false, false, command)
	if got := expandLikeKubernetes(container.Command, container.Env); !slices.Equal(got, command) {
		t.Errorf("debug container runs %q, want %q", got, command)
	}
	pod := newDebugPod("debug-pod", "ns", "image", false, false, command).Spec.Containers[0]
	if got := expandLikeKubernetes(pod.Command, pod.Env); !slices.Equal(got, command) {
		t.Errorf("debug pod runs %q, want %q", got, command)
	}
}

// expandLikeKubernetes expands command as the kubelet expands a container's command: "$$" becomes
// "$", and "$(NAME)" becomes the value of the container's variable NAME, if it has one.
func expandLikeKubernetes(command []string, env []corev1.EnvVar) []string {
	vars := map[string]string{}
	for _, v := range env {
		vars[v.Name] = v.Value
	}
	expanded := make([]string, len(command))
	for i, arg := range command {
		var out strings.Builder
		for j := 0; j < len(arg); j++ {
			if arg[j] != '$' || j+1 == len(arg) {
				out.WriteByte(arg[j])
				continue
			}
			switch arg[j+1] {
			case '$':
				out.WriteByte('$')
				j++
			case '(':
				if end := strings.IndexByte(arg[j:], ')'); end > 0 {
					if value, ok := vars[arg[j+2:j+end]]; ok {
						out.WriteString(value)
						j += end
						continue
					}
				}
				out.WriteByte('$')
			default:
				out.WriteByte('$')
			}
		}
		expanded[i] = out.String()
	}
	return expanded
}
