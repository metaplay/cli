/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows"
)

// isCmdShim reports whether path resolves to a Windows .cmd or .bat script.
// exec.Cmd invokes those via cmd.exe, which shows
// "Terminate batch job (Y/N)?" on Ctrl+C and blocks until answered —
// startCmd uses this to opt into a faster Cancel policy (kill the whole
// process tree atomically) instead of giving the child a graceful window.
func isCmdShim(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".cmd") || strings.HasSuffix(p, ".bat")
}

// startCmd configures cmd.Cancel/WaitDelay, starts cmd inside a Windows
// Job Object, and returns a cleanup function. Closing the cleanup func
// (or letting our process die) atomically terminates the child and every
// descendant via JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
//
// To win the race against descendants spawning before we can put the child
// in the job, the process is created with CREATE_SUSPENDED, attached to
// the job, and only then resumed. Anything the child spawns after that
// inherits the job automatically.
//
// This matters because tools like pnpm/npm are installed as .cmd shims:
// `exec.Command("pnpm", ...)` actually launches cmd.exe to run pnpm.cmd,
// which spawns node.exe, which spawns its own worker processes. CTRL_C_EVENT
// propagation across that chain is unreliable — cmd.exe catches the signal
// and shows "Terminate batch job (Y/N)?" while node descendants stay alive.
// With the job object, killing the parent reliably tears down the whole tree.
//
// cmd.Cancel and cmd.WaitDelay are configured here (rather than by callers)
// so the Cancel closure captures the cleanup func directly — no indirection
// variable, no race window between cmd.Start and a post-hoc setCleanup.
func startCmd(cmd *exec.Cmd) (func(), error) {
	job, jobErr := prepareJob()

	// cleanup is referenced by cmd.Cancel below, so it must be in scope
	// before cmd.Start (which spawns exec.Cmd's ctx-watching goroutine).
	// sync.Once makes it safe to invoke from both Cancel and a deferred
	// caller path without double-closing a recycled handle.
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if jobErr == nil {
				_ = windows.CloseHandle(job)
			}
		})
	}

	// For .cmd/.bat shims (pnpm.cmd, playwright.cmd, ...): close the Job
	// Object immediately on cancel so cmd.exe is killed before it can write
	// "Terminate batch job (Y/N)?" to the console. For native executables
	// (dotnet, docker): no-op Cancel + a 10s WaitDelay so the child can
	// handle its own SIGINT/CTRL_C_EVENT (forwarding to containers, flushing
	// build state, etc.) before being force-killed.
	if isCmdShim(cmd.Path) {
		cmd.Cancel = func() error { cleanup(); return nil }
		cmd.WaitDelay = 2 * time.Second
	} else {
		cmd.Cancel = func() error { return nil }
		cmd.WaitDelay = 10 * time.Second
	}

	if jobErr != nil {
		// Job setup failed; fall through to a plain Start. Descendants
		// may outlive cancellation, but we don't want job-object problems
		// to break the build.
		log.Debug().Err(jobErr).Msg("Job object setup failed; subprocess tree may outlive cancellation")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cleanup, nil
	}

	// Create the child suspended so we can attach it to the job before it
	// can spawn anything.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED

	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}

	pid := uint32(cmd.Process.Pid)
	assignErr := assignProcessToJob(job, pid)

	// We MUST resume the thread no matter what — leaving it suspended hangs
	// cmd.Wait forever. If we couldn't assign to the job, the cleanup func
	// stays a job-closer but the process runs outside the job.
	if err := resumeProcessThreads(pid); err != nil {
		// Truly stuck: process is suspended and we can't unstick it. Best
		// effort is to kill it and Wait so exec.Cmd's internal watchdog
		// goroutine exits.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cleanup()
		return nil, fmt.Errorf("failed to resume suspended child (pid %d): %w", pid, err)
	}

	if assignErr != nil {
		log.Debug().Err(assignErr).Uint32("pid", pid).Msg("AssignProcessToJobObject failed; subprocess tree may outlive cancellation")
		// Close the orphaned job. cmd.Cancel still references our cleanup
		// closure — since the process isn't in the job, the Cancel hook
		// degrades to a no-op (sync.Once swallows the second close).
		cleanup()
		return func() {}, nil
	}

	return cleanup, nil
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
