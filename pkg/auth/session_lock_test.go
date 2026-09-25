/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
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
