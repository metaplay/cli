/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

// Through sh, each argument is the original value, also for a password with shell characters.
func TestMariadbConnectionArgsRoundTripThroughShell(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	password := `p$w'd "x" & $(id) *`
	out, err := exec.Command(shell, "-c", "printf '%s\\n' "+mariadbConnectionArgs("db.local", "user", password)).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "--host=db.local\n--user=user\n--password=" + password + "\n"
	if string(out) != want {
		t.Errorf("arguments = %q, want %q", strings.Split(string(out), "\n"), strings.Split(want, "\n"))
	}
}
