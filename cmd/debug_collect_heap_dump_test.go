/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/metaplay/cli/pkg/kubeutil"
)

// The collector gets the output path as one argument, also when it has spaces or quotes and runs
// through 'su' and 'bash -c' as the server's user.
func TestHeapDumpCollectCommandQuotesPath(t *testing.T) {
	for _, tool := range []string{"sh", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	// Fake tools on PATH: the collectors print their arguments one per line, and 'su USER -c CMD'
	// runs CMD with sh.
	bin := t.TempDir()
	printArgs := "#!/bin/sh\nfor arg; do printf '%s\\n' \"$arg\"; done\n"
	fakeSu := "#!/bin/sh\n[ \"$2\" = -c ] || exit 99\nexec sh -c \"$3\"\n"
	for name, script := range map[string]string{"dotnet-gcdump": printArgs, "dotnet-dump": printArgs, "su": fakeSu} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	outputPath := "/tmp/heap dump it's.gcdump"
	want := "collect\n-p\n42\n-o\n" + outputPath + "\n"
	for _, user := range []string{"root", "app"} {
		for _, mode := range []string{"gcdump", "dump"} {
			command := heapDumpCollectCommand(mode, &kubeutil.ServerProcessInfo{Pid: 42, Username: user}, outputPath)
			cmd := exec.Command("sh", "-c", command)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.Output()
			if err != nil || string(out) != want {
				t.Errorf("user %s, mode %s: %q gave %q, %v; want %q", user, mode, command, out, err, want)
			}
		}
	}
}

// An output file name with a character that removing the file in the pod does not support is a
// usage error. A directory with one is fine: only the file name is used in the pod.
func TestHeapDumpPrepareRejectsUnsupportedFileName(t *testing.T) {
	for _, c := range kubeutil.UnsupportedFileNameChars {
		o := debugCollectHeapDumpOpts{flagCollectMode: "gcdump", flagOutputPath: "dir/heap" + string(c) + ".gcdump"}
		if err := o.Prepare(nil, nil); err == nil {
			t.Errorf("Prepare accepted %q", o.flagOutputPath)
		}
	}
	o := debugCollectHeapDumpOpts{flagCollectMode: "gcdump", flagOutputPath: "my (dir)/heap dump.gcdump"}
	if err := o.Prepare(nil, nil); err != nil {
		t.Errorf("Prepare(%q) = %v, want no error", o.flagOutputPath, err)
	}
}
