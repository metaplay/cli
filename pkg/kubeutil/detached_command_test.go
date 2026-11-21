/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
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
	return func(ctx context.Context, script string) (string, error) {
		out, err := exec.CommandContext(ctx, "sh", "-c", script).Output()
		return string(out), err
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

// Output that was cut short is never taken as the command's state.
func TestRunDetachedRejectsCutOutput(t *testing.T) {
	fastPolls(t)
	polls := 0
	run := func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "setsid") {
			return "", nil // The output of starting it is cut short, which is not an error
		}
		polls++
		return "done 0"[:polls%6], nil // Never the full line
	}
	_, err := runDetached(context.Background(), run, "/work", "true")
	if err == nil {
		t.Fatal("runDetached succeeded on cut output")
	}
	if polls != detachedMaxFailedPolls {
		t.Errorf("polled %d times, want %d", polls, detachedMaxFailedPolls)
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
	}
	for input, want := range valid {
		state, exitCode, err := parseDetachedState(input)
		if err != nil || state != want.state || exitCode != want.exitCode {
			t.Errorf("parseDetachedState(%q) = %q, %d, %v; want %q, %d", input, state, exitCode, err, want.state, want.exitCode)
		}
	}
	for _, input := range []string{"", "done 0", "done \n", "runn", "done 0\ndone 1\n", "running\nextra"} {
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
