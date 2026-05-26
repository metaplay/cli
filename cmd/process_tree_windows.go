/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows"
)

// startCmd starts cmd inside a Windows Job Object so that closing the
// returned cleanup func (or letting our process die) atomically terminates
// the child and every descendant.
//
// To win the race against descendants spawning before we can put the child
// in the job, we create the process with CREATE_SUSPENDED, attach it to
// the job, and only then resume its main thread. Any process spawned by
// the child after that point inherits the job automatically.
//
// This matters because tools like pnpm/npm are installed as .cmd shims:
// `exec.Command("pnpm", ...)` actually launches cmd.exe to run pnpm.cmd,
// which spawns node.exe, which spawns its own worker processes. CTRL_C_EVENT
// propagation across that chain is unreliable — the immediate cmd.exe may
// catch the signal and prompt "Terminate batch job (Y/N)?" while the node
// descendants stay alive as orphans. With the job object, killing the parent
// reliably tears down the whole tree.
func startCmd(cmd *exec.Cmd) (func(), error) {
	job, jobErr := prepareJob()
	if jobErr != nil {
		// Job setup failed; degrade gracefully to a plain Start. Descendants
		// will outlive cancellation, but we don't want job-object problems
		// to break the build.
		log.Debug().Err(jobErr).Msg("Job object setup failed; subprocess tree may outlive cancellation")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return func() {}, nil
	}

	// Create the child suspended so we can attach it to the job before it
	// can spawn anything.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED

	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}

	pid := uint32(cmd.Process.Pid)
	assignErr := assignProcessToJob(job, pid)

	// We MUST resume the thread no matter what — leaving it suspended hangs
	// cmd.Wait forever. If we couldn't assign to the job, the cleanup func
	// becomes a no-op, but the process still runs to completion.
	if err := resumeProcessThreads(pid); err != nil {
		// Truly stuck: process is suspended and we can't unstick it. Best
		// effort is to kill it and Wait so exec.Cmd's internal watchdog
		// goroutine exits (cmd.Wait signals ctxDone; without it the
		// goroutine lives until ctx.Done fires).
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("failed to resume suspended child (pid %d): %w", pid, err)
	}

	if assignErr != nil {
		log.Debug().Err(assignErr).Uint32("pid", pid).Msg("AssignProcessToJobObject failed; subprocess tree may outlive cancellation")
		_ = windows.CloseHandle(job)
		return func() {}, nil
	}

	// Wrap CloseHandle in sync.Once so callers can invoke cleanup more than
	// once (e.g. from both cmd.Cancel and a deferred call) without risking
	// a double-close on a recycled handle.
	var once sync.Once
	return func() { once.Do(func() { _ = windows.CloseHandle(job) }) }, nil
}

// prepareJob creates a Job Object configured to terminate every process in
// the job when the last handle is closed.
func prepareJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return job, nil
}

// assignProcessToJob opens a fresh handle to pid with the rights
// AssignProcessToJobObject needs (Go's os.Process keeps its own handle but
// doesn't expose it) and attaches it.
func assignProcessToJob(job windows.Handle, pid uint32) error {
	proc, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA,
		false,
		pid,
	)
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}

// resumeProcessThreads resumes every thread of pid that is currently
// suspended. We just created the process with CREATE_SUSPENDED so it has a
// single suspended main thread, but enumerating-and-resuming is robust
// against whatever the OS hands us.
func resumeProcessThreads(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	if err := windows.Thread32First(snap, &te); err != nil {
		return fmt.Errorf("Thread32First: %w", err)
	}

	resumed := 0
	for {
		if te.OwnerProcessID == pid {
			th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
			if err == nil {
				_, _ = windows.ResumeThread(th)
				_ = windows.CloseHandle(th)
				resumed++
			}
		}
		if err := windows.Thread32Next(snap, &te); err != nil {
			// Thread32Next returns ERROR_NO_MORE_FILES at the end of the snapshot.
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return fmt.Errorf("Thread32Next: %w", err)
		}
	}
	if resumed == 0 {
		return fmt.Errorf("no threads found for pid %d", pid)
	}
	return nil
}

// wasKilledBySignal: Windows doesn't surface signal-induced termination via
// ProcessState (TerminateProcess sets an arbitrary exit code, and even
// Ctrl+C handlers may set their own). Callers must track forwarded signals
// themselves; this helper exists for cross-platform symmetry.
func wasKilledBySignal(_ *os.ProcessState) bool {
	return false
}
