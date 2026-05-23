/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os/exec"
	"unsafe"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows"
)

// killOnExit attaches cmd.Process to a Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, then returns a cleanup func. When the
// cleanup func runs (or the parent process dies for any reason and the
// handle is released), the kernel terminates every process in the job —
// the child and all of its descendants — atomically.
//
// This matters on Windows because tools like pnpm/npm are installed as
// .cmd shims: `exec.Command("pnpm", ...)` actually launches cmd.exe to
// run pnpm.cmd, which spawns node.exe, which spawns its own worker
// processes. CTRL_C_EVENT propagation across that chain is unreliable —
// the immediate cmd.exe may catch the signal and prompt "Terminate batch
// job (Y/N)?" while the node descendants stay alive as orphans. With the
// job object, killing the parent reliably tears down the whole tree.
//
// Must be called after cmd.Start(). On failure, returns a no-op cleanup
// and logs at debug — the caller continues without job protection.
func killOnExit(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return func() {}
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Debug().Err(err).Msg("CreateJobObject failed; subprocess tree may outlive cancellation")
		return func() {}
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
		log.Debug().Err(err).Msg("SetInformationJobObject failed; subprocess tree may outlive cancellation")
		return func() {}
	}

	// AssignProcessToJobObject needs PROCESS_TERMINATE | PROCESS_SET_QUOTA
	// on the target handle. Go's os.Process keeps its own handle but doesn't
	// expose it, so we open a fresh one by PID.
	proc, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		log.Debug().Err(err).Msg("OpenProcess failed; subprocess tree may outlive cancellation")
		return func() {}
	}
	defer windows.CloseHandle(proc)

	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		log.Debug().Err(err).Msg(fmt.Sprintf("AssignProcessToJobObject failed for pid %d; subprocess tree may outlive cancellation", cmd.Process.Pid))
		return func() {}
	}

	return func() { _ = windows.CloseHandle(job) }
}
