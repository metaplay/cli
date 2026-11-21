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
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
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

// The trailer of WithExitTrailer comes back off the output exactly, with the command's exit code,
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
		out, err := exec.Command(shell, "-c", WithExitTrailer(c.command)).Output()
		if err != nil {
			t.Fatalf("%q: %v", c.command, err)
		}
		stdout, exitCode, err := splitExitTrailer(string(out))
		if err != nil || stdout != c.stdout || exitCode != c.exitCode {
			t.Errorf("%q: got %q, %d, %v; want %q, %d", c.command, stdout, exitCode, err, c.stdout, c.exitCode)
		}
	}
}

// '$$' in the command is the PID of the process that 'exec' replaces the shell with, as in a command
// run by itself. The CPU profile records dotnet-trace's PID so.
func TestExitTrailerKeepsShellPID(t *testing.T) {
	stdout, err := localShell(t)(t.Context(), `echo $$; exec sh -c 'echo $$'`)
	lines := strings.Fields(stdout)
	if err != nil || len(lines) != 2 || lines[0] != lines[1] {
		t.Errorf("PIDs before and after exec = %q, %v; want the same PID twice", stdout, err)
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

// A non-zero exit code is a utilexec.CodeExitError, which callers check for to tell that the
// command ended, and cut output or a failed stream is not.
func TestCheckExecOutputExitCode(t *testing.T) {
	stdout, err := checkExecOutput("out"+execExitMarker+"3\n", nil)
	exitErr, ok := errors.AsType[utilexec.CodeExitError](err)
	if !ok || exitErr.Code != 3 || stdout != "out" {
		t.Errorf("checkExecOutput = %q, %v; want \"out\" and a CodeExitError with code 3", stdout, err)
	}
	if stdout, err := checkExecOutput("out"+execExitMarker+"0\n", nil); err != nil || stdout != "out" {
		t.Errorf("checkExecOutput = %q, %v; want \"out\" and no error", stdout, err)
	}
	for _, c := range []struct {
		raw       string
		streamErr error
	}{{"out", nil}, {"out" + execExitMarker + "3\n", errors.New("stream failed")}} {
		if _, err := checkExecOutput(c.raw, c.streamErr); err == nil {
			t.Errorf("checkExecOutput(%q, %v) succeeded", c.raw, c.streamErr)
		} else if _, ok := errors.AsType[utilexec.CodeExitError](err); ok {
			t.Errorf("checkExecOutput(%q, %v) = %v, want no CodeExitError", c.raw, c.streamErr, err)
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
