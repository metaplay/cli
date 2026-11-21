/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
	"fmt"
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
		out, err := exec.CommandContext(ctx, "sh", "-c", withExitTrailer(script)).Output()
		if err != nil {
			return string(out), err
		}
		stdout, exitCode, err := splitExitTrailer(string(out))
		if err == nil && exitCode != 0 {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return stdout, err
	}
}

func fastPolls(t *testing.T) {
	t.Helper()
	oldFirst, oldInterval := detachedFirstPoll, detachedPollInterval
	detachedFirstPoll, detachedPollInterval = time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { detachedFirstPoll, detachedPollInterval = oldFirst, oldInterval })
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
	_, err := runDetached(context.Background(), localShell(t), t.TempDir(), "kill -9 $PPID; sleep 1")
	if err == nil || !strings.Contains(err.Error(), "without an exit code") {
		t.Errorf("err = %v, want an error about the missing exit code", err)
	}
}

// Failed checks, such as ones whose output was cut short, are retried, up to a limit.
func TestRunDetachedRetriesFailedChecks(t *testing.T) {
	fastPolls(t)
	polls := 0
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "", nil
		}
		polls++
		return "", errOutputCutShort
	}
	_, err := runDetached(context.Background(), run, "/work", "true")
	if !errors.Is(err, errOutputCutShort) {
		t.Errorf("err = %v, want the failed check's error", err)
	}
	if polls != detachedMaxFailedPolls {
		t.Errorf("polled %d times, want %d", polls, detachedMaxFailedPolls)
	}
}

// A command that started although its exec call failed, as when the call's stream ends early, is
// waited for.
func TestRunDetachedWaitsAfterFailedStart(t *testing.T) {
	fastPolls(t)
	shell := localShell(t)
	run := func(ctx context.Context, script string) (string, error) {
		out, err := shell(ctx, script)
		if strings.Contains(script, "setsid") {
			return "", errOutputCutShort
		}
		return out, err
	}
	result, err := runDetached(context.Background(), run, t.TempDir(), "exit 4")
	if err != nil || result.ExitCode != 4 {
		t.Errorf("result = %+v, err = %v; want exit code 4", result, err)
	}

	// A command that did not start reports why its start failed.
	run = func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "", errors.New("pod not found")
		}
		return "not-started\n", nil
	}
	if _, err := runDetached(context.Background(), run, "/work", "true"); err == nil || !strings.Contains(err.Error(), "pod not found") {
		t.Errorf("err = %v, want the start's error", err)
	}
}

func TestRunDetachedStopsWhenCancelled(t *testing.T) {
	fastPolls(t)
	ctx, cancel := context.WithCancel(context.Background())
	run := func(ctx context.Context, script string) (string, error) {
		if !strings.Contains(script, "setsid") {
			cancel()
		}
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
	// The child of 'sleep' ends at once, and sleep does not reap it while it sleeps.
	out, err := run(context.Background(), "sh -c 'true & echo $!; exec sleep 2' & sleep 0.5")
	if err != nil {
		t.Fatal(err)
	}
	zombie := strings.TrimSpace(out)
	check := func(pid string) bool {
		_, err := run(context.Background(), processRunningCheck(pid))
		return err == nil
	}
	if stat, _ := os.ReadFile("/proc/" + zombie + "/stat"); !strings.Contains(string(stat), ") Z") {
		t.Skipf("could not make a zombie: /proc/%s/stat = %q", zombie, stat)
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
