/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
)

// RunDetachedInDebugContainer first checks whether its command ended after detachedFirstPoll, and
// then twice as long after each check, up to detachedPollInterval. A short command does not wait
// for a full interval, and a long one is not checked more than once per interval.
var (
	detachedFirstPoll    = 200 * time.Millisecond
	detachedPollInterval = time.Second
)

// detachedMaxFailedPolls is how many checks in a row may fail, eg, on a network error, before
// RunDetachedInDebugContainer gives up waiting.
const detachedMaxFailedPolls = 5

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
	// Start the command in the background, in its own session, so that it does not depend on the
	// exec call that started it. The exit code goes to 'status' only once the command has ended:
	// 'status.tmp' is renamed, so 'status' is never seen half-written.
	wrapper := "sh -c " + ShellQuote(command) + " </dev/null >/dev/null 2>stderr; echo $? >status.tmp && mv status.tmp status"
	dir := ShellQuote(workDir)
	launch := "mkdir -p " + dir + " && cd " + dir + " && rm -f status status.tmp stderr pid" +
		" && { setsid sh -c " + ShellQuote(wrapper) + " </dev/null >/dev/null 2>&1 & echo $! >pid; }"
	_, launchErr := run(ctx, launch)
	if launchErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("stopped waiting for the command: %w", ctx.Err())
		}
		// The command may have started although the exec call failed: the checks below tell.
		log.Debug().Msgf("Starting a detached command failed: %v", launchErr)
	}

	// Check until the command has ended. The process is checked after 'status', and 'status' once
	// more after the process: the command may end between the checks.
	poll := "cd " + dir + " 2>/dev/null && [ -f pid ] || { echo not-started; exit 0; }; " +
		"if [ -f status ]; then echo \"done $(cat status)\"; " +
		"elif " + processRunningCheck("$(cat pid)") + "; then echo running; " +
		"elif [ -f status ]; then echo \"done $(cat status)\"; " +
		"else echo lost; fi"
	failedPolls := 0
	delay := detachedFirstPoll
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("stopped waiting for the command: %w", ctx.Err())
		case <-time.After(delay):
		}
		delay = min(2*delay, detachedPollInterval)

		stdout, err := run(ctx, poll)
		var state string
		var exitCode int
		if err == nil {
			state, exitCode, err = parseDetachedState(stdout)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("stopped waiting for the command: %w", ctx.Err())
			}
			failedPolls++
			log.Debug().Msgf("Failed to check the detached command (%d/%d): %v", failedPolls, detachedMaxFailedPolls, err)
			if failedPolls >= detachedMaxFailedPolls {
				return nil, fmt.Errorf("failed to check whether the command has ended: %w", err)
			}
			continue
		}
		failedPolls = 0

		switch state {
		case "running":
			continue
		case "done":
			// The command's stderr is only informative, so failing to read it is not an error.
			stderr, err := run(ctx, "tail -c "+strconv.Itoa(detachedStderrTailBytes)+" "+dir+"/stderr")
			if err != nil {
				log.Debug().Msgf("Failed to read the detached command's stderr: %v", err)
			}
			return &DetachedResult{ExitCode: exitCode, Stderr: stderr}, nil
		case "lost":
			return nil, errors.New("the command ended without an exit code: it was killed, eg, for using too much memory")
		default: // "not-started"
			if launchErr != nil {
				return nil, fmt.Errorf("failed to start the command: %w", launchErr)
			}
			return nil, errors.New("the command did not start")
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
