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
	"strconv"
	"strings"
	"testing"
	"time"
)

// localShell runs the scripts of runDetached with the local sh, as the debug container would.
func localShell(t *testing.T) func(ctx context.Context, script string) (string, error) {
	t.Helper()
	for _, tool := range []string{"sh", "setsid"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	// Like ExecInDebugContainer, fail unless the whole stdout arrived and the script exited with 0.
	return func(ctx context.Context, script string) (string, error) {
		out, err := exec.CommandContext(ctx, "sh", "-c", WithExitTrailer(script)).Output()
		return checkExecOutput(string(out), err)
	}
}

func fastPolls(t *testing.T) {
	t.Helper()
	oldFirst, oldInterval, oldFailing, oldTimeout := detachedFirstPoll, detachedPollInterval, detachedMaxFailingTime, detachedExecTimeout
	detachedFirstPoll, detachedPollInterval, detachedMaxFailingTime = time.Millisecond, 10*time.Millisecond, 50*time.Millisecond
	detachedExecTimeout = 5 * time.Second
	t.Cleanup(func() {
		detachedFirstPoll, detachedPollInterval, detachedMaxFailingTime, detachedExecTimeout = oldFirst, oldInterval, oldFailing, oldTimeout
	})
}

func TestRunDetachedReportsExitCodeAndStderr(t *testing.T) {
	fastPolls(t)
	run := localShell(t)
	workDir := filepath.Join(t.TempDir(), "work dir")

	result, err := runDetached(context.Background(), run, workDir, "sleep 0.1; echo data >out.txt; echo warning >&2")
	if err != nil {
		t.Fatalf("runDetached: %v", err)
	}
	if result.ExitCode != 0 || result.Stderr != "warning\n" {
		t.Errorf("result = %+v, want exit code 0 and stderr \"warning\\n\"", result)
	}
	if out, err := os.ReadFile(filepath.Join(workDir, "out.txt")); err != nil || string(out) != "data\n" {
		t.Errorf("out.txt = %q, %v: the command must run in workDir", out, err)
	}

	result, err = runDetached(context.Background(), run, workDir, "echo failed >&2; exit 3")
	if err != nil {
		t.Fatalf("runDetached: %v", err)
	}
	if result.ExitCode != 3 || result.Stderr != "failed\n" {
		t.Errorf("result = %+v, want exit code 3 and stderr \"failed\\n\"", result)
	}
}

// A pipeline fails when its first command fails, with 'set -o pipefail' as the export uses it.
func TestRunDetachedPipefail(t *testing.T) {
	fastPolls(t)
	run := localShell(t)
	if _, err := run(context.Background(), "set -o pipefail"); err != nil {
		t.Skip("the local sh does not support pipefail")
	}
	result, err := runDetached(context.Background(), run, t.TempDir(), "set -o pipefail; false | gzip -c >out.gz")
	if err != nil {
		t.Fatalf("runDetached: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("exit code = 0, want the failed command's exit code")
	}
}

// A command whose wrapper is killed, as by the out-of-memory killer, never writes its exit code.
func TestRunDetachedKilledCommand(t *testing.T) {
	fastPolls(t)
	_, err := runDetached(context.Background(), localShell(t), t.TempDir(), "echo dumping >&2; kill -9 $PPID; sleep 1")
	if !errors.Is(err, ErrDetachedCommandLost) || !strings.Contains(err.Error(), "dumping") {
		t.Errorf("err = %v, want an error about the missing exit code, with the command's stderr", err)
	}
}

// A command whose PID file is empty is reported as lost at once, instead of seeming to run: the
// check does not read an empty PID's '/proc//stat' as /proc/stat. The command runs only after its
// PID was recorded, so it can empty the file.
func TestRunDetachedEmptyPidFile(t *testing.T) {
	fastPolls(t)
	start := time.Now()
	_, err := runDetached(context.Background(), localShell(t), t.TempDir(), `for f in .detached-*/pid; do : >"$f"; done; sleep 3`)
	if !errors.Is(err, ErrDetachedCommandLost) {
		t.Errorf("err = %v, want the command reported as lost", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("reported after %v, want before the command ends after 3 s", elapsed)
	}
}

// Failed checks, such as ones whose output was cut short, are retried until they have failed for
// detachedMaxFailingTime.
func TestRunDetachedRetriesFailedChecks(t *testing.T) {
	fastPolls(t)
	polls := 0
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "started\n", nil
		}
		polls++
		return "", errOutputCutShort
	}
	start := time.Now()
	_, err := runDetached(context.Background(), run, "/work", "true")
	if !errors.Is(err, errOutputCutShort) {
		t.Errorf("err = %v, want the failed check's error", err)
	}
	// How many checks fit in the time depends on the scheduler: a loaded runner can stall for the
	// whole time after the first one.
	if elapsed := time.Since(start); elapsed < detachedMaxFailingTime || polls < 1 {
		t.Errorf("gave up after %v and %d checks, want at least %v", elapsed, polls, detachedMaxFailingTime)
	}

	// A check that works starts the time over: a command whose checks fail now and then is
	// waited for. The time is long enough for 2 failed checks, also with Windows' 15 ms timer.
	detachedMaxFailingTime = time.Second
	polls = 0
	run = func(ctx context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "setsid"):
			return "started\n", nil
		case strings.Contains(script, "tail"):
			return "", nil
		}
		polls++
		switch {
		case polls >= 40:
			return "done 0\n", nil
		case polls%3 == 0:
			time.Sleep(10 * time.Millisecond)
			return "running\n", nil
		default:
			time.Sleep(10 * time.Millisecond)
			return "", errOutputCutShort
		}
	}
	if result, err := runDetached(context.Background(), run, "/work", "true"); err != nil || result.ExitCode != 0 {
		t.Errorf("result = %+v, err = %v; want exit code 0", result, err)
	}
}

// A check that hangs, as on a connection that stalls, is given up after detachedExecTimeout and
// counts as failed, so a stalled connection cannot keep the wait going forever.
func TestRunDetachedTimesOutHungChecks(t *testing.T) {
	fastPolls(t)
	detachedExecTimeout = 20 * time.Millisecond
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "started\n", nil
		}
		<-ctx.Done() // Hangs until the call's timeout
		return "", ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := runDetached(context.Background(), run, "/work", "true")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want the checks' timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDetached still waits on hung checks")
	}
}

// After a failed start, the checks do not report how an earlier command in the same workDir ended.
func TestRunDetachedIgnoresEarlierCommand(t *testing.T) {
	fastPolls(t)
	shell := localShell(t)
	workDir := t.TempDir()
	if _, err := runDetached(context.Background(), shell, workDir, "true"); err != nil {
		t.Fatalf("runDetached: %v", err)
	}
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "", errors.New("pod not found")
		}
		return shell(ctx, script)
	}
	if result, err := runDetached(context.Background(), run, workDir, "exit 5"); err == nil || !strings.Contains(err.Error(), "pod not found") {
		t.Errorf("result = %+v, err = %v; want the start's error", result, err)
	}
}

// A launch whose exec call failed, as when the call's stream ends early, is run again, and the
// command, which the first launch started, runs once and is waited for.
func TestRunDetachedWaitsAfterFailedStart(t *testing.T) {
	fastPolls(t)
	shell := localShell(t)
	launches := 0
	run := func(ctx context.Context, script string) (string, error) {
		out, err := shell(ctx, script)
		if strings.Contains(script, "setsid") {
			launches++
			if launches == 1 {
				return "", errOutputCutShort
			}
		}
		return out, err
	}
	workDir := t.TempDir()
	result, err := runDetached(context.Background(), run, workDir, "echo ran >>runs; exit 4")
	if err != nil || result.ExitCode != 4 {
		t.Errorf("result = %+v, err = %v; want exit code 4", result, err)
	}
	if runs, err := os.ReadFile(filepath.Join(workDir, "runs")); err != nil || string(runs) != "ran\n" || launches != 2 {
		t.Errorf("runs = %q, %v after %d launches; want the command run once after 2 launches", runs, err, launches)
	}

	// A command that cannot be started reports why its start failed.
	run = func(ctx context.Context, script string) (string, error) {
		return "", errors.New("pod not found")
	}
	if _, err := runDetached(context.Background(), run, "/work", "true"); err == nil || !strings.Contains(err.Error(), "pod not found") {
		t.Errorf("err = %v, want the start's error", err)
	}
}

// A launch that cannot create the control directory, here in a read-only workDir, waits for a PID
// from another launch, and reports the command as not started when none comes.
func TestRunDetachedReadOnlyWorkDir(t *testing.T) {
	fastPolls(t)
	detachedExecTimeout = 400 * time.Millisecond // The launch waits for half of it
	run := localShell(t)
	workDir := t.TempDir()
	if err := os.Chmod(workDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(workDir, 0o755) })
	if f, err := os.Create(filepath.Join(workDir, "probe")); err == nil {
		_ = f.Close()
		t.Skip("the directory is writable anyway, eg, as root")
	}
	if _, err := runDetached(context.Background(), run, workDir, "true"); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Errorf("err = %v, want the command reported as not started", err)
	}
}

// A launch that finds no PID after the command's wrapper ended reports that the command did not
// start, as when the wrapper cannot record its PID on a full disk.
func TestRunDetachedReportsNotStarted(t *testing.T) {
	fastPolls(t)
	run := func(ctx context.Context, script string) (string, error) {
		return "not-started\n", nil
	}
	if _, err := runDetached(context.Background(), run, "/work", "true"); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Errorf("err = %v, want the command reported as not started", err)
	}
}

func TestRunDetachedStopsWhenCancelled(t *testing.T) {
	fastPolls(t)
	ctx, cancel := context.WithCancel(context.Background())
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "started\n", nil
		}
		cancel()
		return "running\n", nil
	}
	_, err := runDetached(ctx, run, "/work", "true")
	if err == nil || ctx.Err() == nil {
		t.Errorf("err = %v, want an error after cancellation", err)
	}
}

func TestParseDetachedState(t *testing.T) {
	valid := map[string]struct {
		state    string
		exitCode int
	}{
		"running\n":     {"running", 0},
		"lost\n":        {"lost", 0},
		"not-started\n": {"not-started", 0},
		"done 0\n":      {"done", 0},
		"done 137\n":    {"done", 137},
		"done 0":        {"done", 0},
	}
	for input, want := range valid {
		state, exitCode, err := parseDetachedState(input)
		if err != nil || state != want.state || exitCode != want.exitCode {
			t.Errorf("parseDetachedState(%q) = %q, %d, %v; want %q, %d", input, state, exitCode, err, want.state, want.exitCode)
		}
	}
	for _, input := range []string{"", "done \n", "runn", "done x\n", "done 0\ndone 1\n", "running\nextra"} {
		if _, _, err := parseDetachedState(input); err == nil {
			t.Errorf("parseDetachedState(%q) succeeded, want an error", input)
		}
	}
}

// A zombie, a process that has ended but was not reaped, does not run. The debug pod's PID 1 does
// not reap orphans, so a killed command stays a zombie.
func TestProcessRunningCheck(t *testing.T) {
	run := localShell(t)
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	// The child of 'sleep' ends after the shell has become sleep, which does not reap it. Had it
	// ended before, the shell could have reaped it. The sleeping process writes nothing to stdout,
	// so the run returns while it sleeps: holding stdout open, it would make the run wait until it
	// had ended and the zombie had been reaped.
	pidFile := filepath.Join(t.TempDir(), "pid")
	_, err := run(context.Background(), "sh -c 'sleep 0.2 & echo $! >"+pidFile+"; exec sleep 5' >/dev/null 2>&1 & sleep 0.6")
	if err != nil {
		t.Fatal(err)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	zombie := strings.TrimSpace(string(pidBytes))
	check := func(pid string) bool {
		_, err := run(context.Background(), processRunningCheck(pid))
		return err == nil
	}
	if stat, _ := os.ReadFile("/proc/" + zombie + "/stat"); !strings.Contains(string(stat), ") Z") {
		t.Fatalf("could not make a zombie: /proc/%s/stat = %q", zombie, stat)
	}
	if check(zombie) {
		t.Errorf("zombie %s counts as running", zombie)
	}
	if !check(strconv.Itoa(os.Getpid())) {
		t.Error("the test process does not count as running")
	}
	if check("999999999") {
		t.Error("a missing process counts as running")
	}
}
