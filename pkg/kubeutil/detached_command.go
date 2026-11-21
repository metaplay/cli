/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	utilexec "k8s.io/client-go/util/exec"
)

// RunDetachedInDebugContainer first checks whether its command ended after detachedFirstPoll, and
// then twice as long after each check, up to detachedPollInterval. A short command does not wait
// for a full interval, and a long one is not checked more than once per interval.
var (
	detachedFirstPoll    = 200 * time.Millisecond
	detachedPollInterval = time.Second
)

// detachedMaxFailingTime is how long checks may keep failing, eg, on a network error, before
// RunDetachedInDebugContainer gives up waiting. A connection that drops for a while does not end the
// wait for a command that may still run fine.
var detachedMaxFailingTime = time.Minute

// detachedExecTimeout limits each of RunDetachedInDebugContainer's short exec calls. A call on a
// connection that stalls without failing would otherwise block for as long as the connection
// lasts, and only calls that return count toward detachedMaxFailingTime.
var detachedExecTimeout = 30 * time.Second

// detachedHeartbeatTimeout is how long a detached command runs on after the last check: each check
// touches a heartbeat file, and the command's wrapper stops the command once the file is older.
// Then a command does not go on, eg, reading a database, after the CLI that waited for it died
// without its cleanup. It is longer than detachedMaxFailingTime plus detachedExecTimeout, after
// which a CLI gives up waiting anyway.
var detachedHeartbeatTimeout = 3 * time.Minute

// ErrDetachedCommandLost is the error of a command that ended without writing its exit code. Writing
// it fails when the disk is full.
var ErrDetachedCommandLost = errors.New("the command ended without writing its exit code: it was killed, eg, for using too much memory, or the disk is full")

// DetachedResult is how a command run with RunDetachedInDebugContainer ended.
type DetachedResult struct {
	ExitCode int    // Exit code of the command
	Stderr   string // The end of the command's stderr
}

// detachedStderrTailBytes is how much of the end of the command's stderr DetachedResult has.
const detachedStderrTailBytes = 4000

// RunDetachedInDebugContainer runs command with /bin/sh in the debug container, in workDir, and
// waits until it ends. The command runs detached from any exec stream: it writes its exit code to
// a file, which is polled with short exec calls. So a long command does not depend on one stream
// lasting until it ends, and a stream that ends early cannot make a command that failed or was cut
// short look successful. Anything the command must hand back, it writes to files in workDir. Its
// stdout is discarded.
//
// When ctx is done, the wait ends, but the command goes on until it ends or the pod is deleted.
func RunDetachedInDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, workDir, command string) (*DetachedResult, error) {
	run := func(ctx context.Context, script string) (string, error) {
		stdout, _, err := ExecInDebugContainer(ctx, kubeCli, podName, containerName, script)
		return stdout, err
	}
	return runDetached(ctx, run, workDir, command)
}

// runDetached is RunDetachedInDebugContainer with the exec call replaced by run, for tests. run
// must fail unless the script's whole stdout arrived, as ExecInDebugContainer does.
func runDetached(ctx context.Context, run func(ctx context.Context, script string) (string, error), workDir, command string) (*DetachedResult, error) {
	runWithTimeout := func(ctx context.Context, script string) (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, detachedExecTimeout)
		defer cancel()
		return run(callCtx, script)
	}

	// The command's pid, stderr and exit code go to a directory of this call's own, so that the
	// checks below cannot see the files of an earlier command in workDir: after a failed start,
	// they would report how that command ended.
	ctl := ".detached-" + rand.Text()
	dir := ShellQuote(workDir)

	// Start the command in the background, in its own session, so that it does not depend on the
	// exec call that started it. The wrapper records its PID before it runs the command, and the
	// exit code once the command has ended. Both are written to a temporary file and renamed, so
	// they are never seen half-written. A wrapper that cannot record its PID, eg, on a full disk,
	// does not run the command. While the command runs, the wrapper stops its process group, the
	// command and itself, once the heartbeat is older than detachedHeartbeatTimeout.
	heartbeatSeconds := strconv.Itoa(int(detachedHeartbeatTimeout.Seconds()))
	wrapper := "echo $$ >" + ctl + "/pid.tmp && mv " + ctl + "/pid.tmp " + ctl + "/pid || exit; " +
		"touch " + ctl + "/heartbeat; " +
		"sh -c " + ShellQuote(command) + " </dev/null >/dev/null 2>" + ctl + "/stderr & c=$!; " +
		"while " + processRunningCheck("$c") + "; do " +
		"[ $(( $(date +%s) - $(stat -c %Y " + ctl + "/heartbeat) )) -gt " + heartbeatSeconds + " ] && kill -TERM -$$; " +
		"sleep 0.2; done; wait $c; " +
		"echo $? >" + ctl + "/status.tmp && mv " + ctl + "/status.tmp " + ctl + "/status"
	// The launch prints whether the command started: it waits until the wrapper has recorded its
	// PID or has ended. Creating the control directory makes the launch start the command at most
	// once, so a launch whose exec call failed can run again. A launch that finds the directory
	// waits for the PID of the launch that created it, for half of the exec call's time limit.
	waitSteps := strconv.Itoa(int(detachedExecTimeout / 2 / (10 * time.Millisecond)))
	launch := "mkdir -p " + dir + " && cd " + dir + " || exit; " +
		"if mkdir " + ctl + " 2>/dev/null; then " +
		"{ setsid sh -c " + ShellQuote(wrapper) + " </dev/null >/dev/null 2>&1 & }; w=$!; " +
		"while [ ! -f " + ctl + "/pid ] && " + processRunningCheck("$w") + "; do sleep 0.01; done; " +
		"else i=0; while [ ! -f " + ctl + "/pid ] && [ $i -lt " + waitSteps + " ]; do sleep 0.01; i=$((i+1)); done; fi; " +
		"if [ -f " + ctl + "/pid ]; then echo started; else echo not-started; fi"
	stopped := func() error { return fmt.Errorf("stopped waiting for the command: %w", ctx.Err()) }
	if err := startDetached(ctx, runWithTimeout, launch); err != nil {
		if ctx.Err() != nil {
			return nil, stopped()
		}
		return nil, err
	}

	// Check until the command has ended. The process is checked after 'status', and 'status' once
	// more after the process: the command may end between the checks. A PID that is not a number
	// counts as a process that does not run: empty, '/proc//stat' would name /proc/stat.
	poll := "cd " + dir + "/" + ctl + " 2>/dev/null && [ -f pid ] || { echo not-started; exit 0; }; touch heartbeat; " +
		"pid=$(cat pid); case $pid in ''|*[!0-9]*) pid=invalid;; esac; " +
		"if [ -f status ]; then echo \"done $(cat status)\"; " +
		"elif " + processRunningCheck("$pid") + "; then echo running; " +
		"elif [ -f status ]; then echo \"done $(cat status)\"; " +
		"else echo lost; fi"
	// The command's stderr is only informative, so failing to read it is not an error.
	readStderr := func() string {
		stderr, err := runWithTimeout(ctx, "tail -c "+strconv.Itoa(detachedStderrTailBytes)+" "+dir+"/"+ctl+"/stderr")
		if err != nil {
			log.Debug().Msgf("Failed to read the detached command's stderr: %v", err)
		}
		return stderr
	}

	var failingSince time.Time // When the checks started failing, or zero if the last one worked
	delay := detachedFirstPoll
	for {
		select {
		case <-ctx.Done():
			return nil, stopped()
		case <-time.After(delay):
		}
		delay = min(2*delay, detachedPollInterval)

		stdout, err := runWithTimeout(ctx, poll)
		var state string
		var exitCode int
		if err == nil {
			state, exitCode, err = parseDetachedState(stdout)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, stopped()
			}
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			log.Debug().Msgf("Failed to check the detached command (failing for %v): %v", time.Since(failingSince).Round(time.Second), err)
			if time.Since(failingSince) >= detachedMaxFailingTime {
				return nil, fmt.Errorf("failed to check whether the command has ended for %v: %w", detachedMaxFailingTime, err)
			}
			continue
		}
		failingSince = time.Time{}

		switch state {
		case "running":
			continue
		case "done":
			return &DetachedResult{ExitCode: exitCode, Stderr: readStderr()}, nil
		case "lost":
			err := ErrDetachedCommandLost
			if stderr := strings.TrimSpace(readStderr()); stderr != "" {
				err = fmt.Errorf("%w; its stderr ends with: %s", err, stderr)
			}
			return nil, err
		default: // "not-started": the command's files are gone, eg, removed in the pod
			return nil, errors.New("the command's PID file is gone")
		}
	}
}

// startDetached runs runDetached's launch script until it reports whether the command started.
// A launch whose exec call failed is run again, until the calls have failed for
// detachedMaxFailingTime: the script starts the command at most once.
func startDetached(ctx context.Context, run func(ctx context.Context, script string) (string, error), launch string) error {
	var failingSince time.Time
	for {
		stdout, err := run(ctx, launch)
		if err == nil {
			switch strings.TrimSuffix(stdout, "\n") {
			case "started":
				return nil
			case "not-started":
				return errors.New("the command did not start: it could not record its PID, eg, on a full disk")
			}
			err = fmt.Errorf("unexpected output from starting the command: %q", stdout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The script ran and failed, eg, on a work directory it cannot create: it would fail again
		if _, exited := errors.AsType[utilexec.CodeExitError](err); exited {
			return fmt.Errorf("failed to start the command: %w", err)
		}
		if failingSince.IsZero() {
			failingSince = time.Now()
		}
		log.Debug().Msgf("Failed to start a detached command (failing for %v): %v", time.Since(failingSince).Round(time.Second), err)
		if time.Since(failingSince) >= detachedMaxFailingTime {
			return fmt.Errorf("failed to start the command: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(detachedFirstPoll):
		}
	}
}

// processRunningCheck is a shell condition that is true while the process with the given PID runs.
// A process that has ended but was not reaped, a zombie, does not run. 'kill -0' succeeds for a
// zombie, and a debug pod's PID 1, such as 'sleep', does not reap the orphaned processes that are
// left to it, so a killed command would stay a zombie and seem to run until the pod is deleted.
// The state is the field after the parenthesized command name in /proc/<pid>/stat.
func processRunningCheck(pid string) string {
	return "{ state=$(sed 's/.*) //' /proc/\"" + pid + "\"/stat 2>/dev/null | cut -c1); [ -n \"$state\" ] && [ \"$state\" != Z ]; }"
}

// parseDetachedState parses the output of runDetached's check, returning the state and, when the
// state is "done", the command's exit code.
func parseDetachedState(stdout string) (string, int, error) {
	state := strings.TrimSuffix(stdout, "\n")
	switch state {
	case "running", "lost", "not-started":
		return state, 0, nil
	}
	if code, ok := strings.CutPrefix(state, "done "); ok {
		if exitCode, err := strconv.Atoi(code); err == nil {
			return "done", exitCode, nil
		}
	}
	return "", 0, fmt.Errorf("unexpected output from checking the command: %q", stdout)
}
