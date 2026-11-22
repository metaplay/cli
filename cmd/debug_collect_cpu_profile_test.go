/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/metaplay/cli/pkg/kubeutil"
)

func TestFormatTraceDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{1, "00:00:01"},
		{30, "00:00:30"},
		{90, "00:01:30"},
		{3599, "00:59:59"},
		{3600, "01:00:00"}, // The maximum --duration. Was formatted as 00:60:00, which dotnet-trace rejects.
	}
	for _, tt := range tests {
		if got := formatTraceDuration(tt.seconds); got != tt.want {
			t.Errorf("formatTraceDuration(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

// fakeTraceTools puts a fake 'dotnet-trace' and 'su' first on PATH. The fake dotnet-trace writes
// its arguments and its PID to files in dir, and runs until SIGINT, like the real one with a long
// --duration. The fake su runs its -c command with sh, as su does with the user's shell.
func fakeTraceTools(t *testing.T) (dir string) {
	t.Helper()
	// On Windows, Git's bash can be on PATH, but it takes neither Windows paths nor Windows PIDs.
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix shell and /proc")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	// A SIGINT ignored by the test process would be ignored by the fake dotnet-trace too, which
	// cannot trap it then. Handling it here gives the fake the default disposition.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	t.Cleanup(func() { signal.Stop(interrupts) })
	dir = t.TempDir()
	scripts := map[string]string{
		"dotnet-trace": "#!/bin/bash\n" +
			"echo \"$@\" > " + dir + "/args\n" +
			"echo $$ > " + dir + "/self-pid\n" +
			"trap 'kill $!; echo interrupted > " + dir + "/stopped; exit 0' INT\n" +
			"sleep 60 & wait\n",
		"su": "#!/bin/sh\n" +
			"[ \"$2\" = -c ] || exit 1\n" +
			"exec sh -c \"$3\"\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// readWhenWritten returns the trimmed contents of path once it is not empty.
func readWhenWritten(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not written", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The collect command records dotnet-trace's own PID, not that of a shell or su around it, also
// when it runs as another user, and the stop command ends dotnet-trace with SIGINT.
func TestTraceCollectCommandStops(t *testing.T) {
	for _, username := range []string{"root", "metaplay"} {
		t.Run(username, func(t *testing.T) {
			dir := fakeTraceTools(t)
			pidPath := dir + "/profile.pid"
			processInfo := &kubeutil.ServerProcessInfo{Pid: 7, Username: username}
			opts := &debugCollectCPUProfileOpts{flagFormat: "Speedscope", flagDuration: 3600, extrArgs: []string{"--providers", "a b;c's $(x)"}}
			command := opts.traceCollectCommand(processInfo, "/tmp/profile.nettrace", pidPath)

			collect := exec.Command("bash", "-c", command)
			if err := collect.Start(); err != nil {
				t.Fatal(err)
			}
			ended := make(chan error, 1)
			go func() { ended <- collect.Wait() }()
			defer func() { _ = collect.Process.Kill() }()

			selfPid := readWhenWritten(t, dir+"/self-pid")
			if got := readWhenWritten(t, pidPath); got != selfPid {
				t.Errorf("PID file = %s, want dotnet-trace's PID %s", got, selfPid)
			}
			wantArgs := "collect -p 7 --format Speedscope --duration 01:00:00 -o /tmp/profile.nettrace --providers a b;c's $(x)"
			if got := readWhenWritten(t, dir+"/args"); got != wantArgs {
				t.Errorf("dotnet-trace arguments = %q, want %q", got, wantArgs)
			}

			if out, err := exec.Command("bash", "-c", stopTraceCommand(pidPath, "/tmp/profile.nettrace")).CombinedOutput(); err != nil {
				t.Fatalf("stop command failed: %v: %s", err, out)
			}
			select {
			case err := <-ended:
				if err != nil {
					t.Errorf("collect command failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the collect command did not end after the stop command")
			}
			if got := readWhenWritten(t, dir+"/stopped"); got != "interrupted" {
				t.Errorf("dotnet-trace was not stopped with SIGINT")
			}
		})
	}
}

// Once dotnet-trace has exited, its PID can belong to another process, which the stop command
// leaves alone.
func TestStopTraceCommandSparesOtherProcess(t *testing.T) {
	dir := fakeTraceTools(t)
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill() }()
	pidPath := dir + "/profile.pid"
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(other.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("bash", "-c", stopTraceCommand(pidPath, "/tmp/profile.nettrace")).CombinedOutput(); err != nil {
		t.Fatalf("stop command failed: %v: %s", err, out)
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the stop command signalled a process that is not dotnet-trace: %v", err)
	}
}
