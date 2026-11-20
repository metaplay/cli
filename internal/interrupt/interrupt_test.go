/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package interrupt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// Trigger cancels the context with ErrInterrupted as the cause, and tells the user once.
func TestTriggerCancelsWithCause(t *testing.T) {
	notices := 0
	ctx := WithCancel(context.Background(), func() { notices++ })

	Trigger(ctx)
	Trigger(ctx)

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
	if cause := context.Cause(ctx); cause != ErrInterrupted { //nolint:errorlint // The exact cause
		t.Errorf("cause = %v, want ErrInterrupted", cause)
	}
	if notices != 1 {
		t.Errorf("notice shown %d times, want 1", notices)
	}
}

// A context derived from the interrupt context triggers the same interrupt.
func TestTriggerThroughDerivedContext(t *testing.T) {
	ctx := WithCancel(context.Background(), nil)
	derived, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	Trigger(derived)

	if ctx.Err() == nil {
		t.Error("the interrupt context was not cancelled")
	}
}

// Trigger does nothing to a context this package did not make.
func TestTriggerWithoutSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Trigger(ctx)
	HoldNotice(ctx)()

	if ctx.Err() != nil {
		t.Errorf("ctx.Err() = %v, want nil", ctx.Err())
	}
}

// An error that wraps ErrInterrupted counts as a cancellation.
func TestErrInterruptedIsCanceled(t *testing.T) {
	if !errors.Is(ErrInterrupted, context.Canceled) {
		t.Error("errors.Is(ErrInterrupted, context.Canceled) = false")
	}
	if plain := fmt.Errorf("stopped: %w", context.Canceled); errors.Is(plain, ErrInterrupted) {
		t.Error("a plain cancellation counts as an interrupt")
	}
}

// The notice is held back while a display is on screen, and the context is cancelled meanwhile.
func TestHoldNotice(t *testing.T) {
	notices := 0
	ctx := WithCancel(context.Background(), func() { notices++ })

	releaseOuter := HoldNotice(ctx)
	releaseInner := HoldNotice(ctx)
	Trigger(ctx)
	if ctx.Err() == nil {
		t.Error("context not cancelled while the notice is held back")
	}
	releaseInner()
	releaseInner()
	if notices != 0 {
		t.Fatalf("notice shown %d times while held back, want 0", notices)
	}
	releaseOuter()
	if notices != 1 {
		t.Errorf("notice shown %d times after the release, want 1", notices)
	}
}

// Without an interrupt, releasing a hold shows nothing.
func TestHoldNoticeWithoutInterrupt(t *testing.T) {
	notices := 0
	ctx := WithCancel(context.Background(), func() { notices++ })

	HoldNotice(ctx)()

	if notices != 0 {
		t.Errorf("notice shown %d times, want 0", notices)
	}
}

// SIGINT cancels the context as Trigger does.
func TestNotifyContextCancelsOnSignal(t *testing.T) {
	notified := make(chan struct{})
	ctx, stop := NotifyContext(context.Background(), func() { close(notified) })
	defer stop()

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.SIGINT); err != nil {
		t.Skipf("cannot send SIGINT to the test process: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("context not cancelled by SIGINT")
	}
	if cause := context.Cause(ctx); cause != ErrInterrupted { //nolint:errorlint // The exact cause
		t.Errorf("cause = %v, want ErrInterrupted", cause)
	}
	<-notified
}

// stop cancels the context without an interrupt.
func TestNotifyContextStop(t *testing.T) {
	notices := 0
	ctx, stop := NotifyContext(context.Background(), func() { notices++ })

	stop()

	if ctx.Err() == nil {
		t.Error("context not cancelled by stop")
	}
	if errors.Is(context.Cause(ctx), ErrInterrupted) {
		t.Error("stop counts as an interrupt")
	}
	if notices != 0 {
		t.Errorf("notice shown %d times, want 0", notices)
	}
}
