/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"testing"
)

// Crossing chart v0.8.0 in either direction uninstalls the existing release
// first. A local chart is taken for a recent one, and an existing version that
// does not parse for the old operator.
func TestMustUninstallExistingRelease(t *testing.T) {
	tests := []struct {
		name      string
		existing  string
		new       string
		uninstall bool
		wantErr   bool
	}{
		{"recent over recent", "0.9.0", "0.10.0", false, false},
		{"old over old", "0.7.2", "0.7.3", false, false},
		{"recent over old", "0.7.2", "0.8.0", true, false},
		{"old over recent", "0.8.0", "0.7.2", true, false},
		{"local over recent", "0.10.0", "local", false, false},
		{"local over old", "0.7.2", "local", true, false},
		{"recent over an existing version that does not parse", "unknown", "0.10.0", true, false},
		{"local over an existing version that does not parse", "unknown", "local", true, false},
		{"a new version that does not parse", "0.10.0", "unknown", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			uninstall, err := mustUninstallExistingRelease(test.existing, test.new)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error for new version %q", test.new)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if uninstall != test.uninstall {
				t.Errorf("uninstall = %v, want %v", uninstall, test.uninstall)
			}
		})
	}
}
