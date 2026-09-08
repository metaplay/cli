//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

// TestIsLockContention pins the contention classification: ERROR_SHARING_VIOLATION
// (CreateFile against metaplay-bridge.ps1's FileShare.None handle) and
// ERROR_LOCK_VIOLATION (LockFileEx against a held lock) must both be retried, and
// unrelated errors must not be.
func TestIsLockContention(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"sharing violation", windows.ERROR_SHARING_VIOLATION, true},
		{"lock violation", windows.ERROR_LOCK_VIOLATION, true},
		{"wrapped sharing violation", fmt.Errorf("open: %w", windows.ERROR_SHARING_VIOLATION), true},
		{"wrapped lock violation", fmt.Errorf("lock: %w", windows.ERROR_LOCK_VIOLATION), true},
		{"access denied", windows.ERROR_ACCESS_DENIED, false},
		{"not found", windows.ERROR_FILE_NOT_FOUND, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isLockContention(test.err); got != test.want {
				t.Errorf("isLockContention(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}
