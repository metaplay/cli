/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A second taker waits for the lock to be released, and then gets it.
func TestLockSessionStore_WaitsForTheHolder(t *testing.T) {
	redirectConfigHome(t)

	unlock, err := lockSessionStore()
	if err != nil {
		t.Fatalf("lockSessionStore: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		unlockSecond, err := lockSessionStore()
		if err == nil {
			unlockSecond()
		}
		acquired <- err
	}()

	select {
	case <-acquired:
		t.Fatal("took the lock while it was held")
	case <-time.After(200 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("lockSessionStore after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting after the lock was released")
	}
}

// A holder that never lets go is reported, not waited on forever.
func TestLockSessionStore_GivesUpOnAHolderThatDoesNotRelease(t *testing.T) {
	redirectConfigHome(t)
	previous := sessionLockTimeout
	sessionLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sessionLockTimeout = previous })

	unlock, err := lockSessionStore()
	if err != nil {
		t.Fatalf("lockSessionStore: %v", err)
	}
	defer unlock()

	if unlockSecond, err := lockSessionStore(); err == nil {
		unlockSecond()
		t.Fatal("took the lock while it was held")
	}
}

// A lock file this user cannot write, such as one root created under sudo,
// still serves: a lock needs the file only open for reading.
func TestLockSessionStore_UsesALockFileItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file its owner cannot write")
	}
	configPath := redirectConfigHome(t)
	if err := os.WriteFile(configPath+".lock", nil, 0444); err != nil {
		t.Fatalf("failed to create the lock file: %v", err)
	}

	unlock, err := lockSessionStore()
	if err != nil {
		t.Fatalf("lockSessionStore: %v", err)
	}
	defer unlock()

	// And it is a lock: a second taker waits for it.
	previous := sessionLockTimeout
	sessionLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sessionLockTimeout = previous })
	if unlockSecond, err := lockSessionStore(); err == nil {
		unlockSecond()
		t.Fatal("took the lock while it was held")
	}
}

// A config directory no lock file can be made in, such as a read-only mount,
// is used unlocked, as it was before the lock, rather than not at all.
func TestLockSessionStore_GoesWithoutWhereNoLockFileCanBeMade(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory its owner cannot write")
	}
	configDir := filepath.Dir(redirectConfigHome(t))
	if err := os.Chmod(configDir, 0500); err != nil {
		t.Fatalf("failed to make the config directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDir, 0700) })

	unlock, err := lockSessionStore()
	if err != nil {
		t.Fatalf("lockSessionStore: %v", err)
	}
	unlock()
}
