/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLockFileExclusive_ContentionTimesOut verifies that a second acquisition of the
// same lock file times out with the readable "another run holds" error instead of
// failing fast or deadlocking: the CLI command and the SDK's metaplay-bridge script
// exclude each other through this lock.
func TestLockFileExclusive_ContentionTimesOut(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "metaplay-bridge.lock")

	release, err := lockFileExclusive(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("first lockFileExclusive returned error: %v", err)
	}
	defer release()

	start := time.Now()
	_, err = lockFileExclusive(lockPath, 1*time.Second)
	if err == nil {
		t.Fatal("expected the second lockFileExclusive to fail while the lock is held")
	}
	elapsed := time.Since(start)
	if !strings.Contains(err.Error(), "another metaplay-bridge run holds") {
		t.Errorf("unexpected error for a contended lock: %v", err)
	}
	if elapsed < 500*time.Millisecond {
		t.Errorf("the contended lock failed after only %s; it should retry until the timeout", elapsed)
	}
}

// TestLockFileExclusive_AcquireAfterRelease verifies that the lock is reusable after
// release (successive build chains must not be blocked by a stale local state).
func TestLockFileExclusive_AcquireAfterRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "metaplay-bridge.lock")

	release, err := lockFileExclusive(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("first lockFileExclusive returned error: %v", err)
	}
	release()

	release2, err := lockFileExclusive(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("second lockFileExclusive after release returned error: %v", err)
	}
	release2()
}
