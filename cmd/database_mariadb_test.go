/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"slices"
	"testing"
)

// Each value is one argument, as it is: no shell runs the command, so nothing is quoted.
func TestMariadbCommand(t *testing.T) {
	password := `p$w'd "x" & $(id) *`
	got := mariadbCommand("db.local", "user", password, "-N", "game_db")
	want := []string{"mariadb", "--host=db.local", "--user=user", "--password=" + password, "-N", "game_db"}
	if !slices.Equal(got, want) {
		t.Errorf("mariadbCommand = %q, want %q", got, want)
	}
}
