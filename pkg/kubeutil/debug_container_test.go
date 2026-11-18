/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// shellQuote's output, run through bash, is the original string, so 'rm -f' gets exactly one
// argument even for names with spaces, wildcards or quotes.
func TestShellQuoteRoundTripsThroughBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	names := []string{"/tmp/dump.gcdump", "/tmp/*", "/tmp/heap dump.gcdump", "/tmp/it's", "/tmp/$(id)", "-rf"}
	for _, name := range names {
		out, err := exec.Command(bash, "-c", "printf '%s\\n' "+shellQuote(name)).Output()
		if err != nil {
			t.Fatalf("bash failed for %q: %v", name, err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != name {
			t.Errorf("shellQuote(%q) through bash = %q", name, got)
		}
	}
}

// The continued context outlives the parent's cancellation by the grace period, and no longer.
func TestContinueAfterCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	continued, cancel := ContinueAfterCancel(parent, 100*time.Millisecond)
	defer cancel()

	cancelParent()
	time.Sleep(20 * time.Millisecond)
	if err := continued.Err(); err != nil {
		t.Fatalf("continued context cancelled with the parent: %v", err)
	}
	select {
	case <-continued.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("continued context not cancelled after the grace period")
	}
}

// Without a cancellation of the parent, only the returned cancel function ends the context.
func TestContinueAfterCancelWithoutParentCancel(t *testing.T) {
	continued, cancel := ContinueAfterCancel(context.Background(), time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if err := continued.Err(); err != nil {
		t.Fatalf("continued context cancelled without a parent cancellation: %v", err)
	}
	cancel()
	if continued.Err() == nil {
		t.Fatal("cancel did not cancel the context")
	}
}
