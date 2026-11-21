/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package interrupt stops a command when the user interrupts it: with SIGINT or SIGTERM, or with a
// Ctrl-C key press in a TUI. The terminal sends no SIGINT for Ctrl-C while a TUI holds it in raw
// mode, so the task runner calls Trigger on that key press. Both cancel the command's context. A
// dialog has no work to stop, so on that key press it only returns ErrInterrupted.
package interrupt

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// ErrInterrupted is the cancellation cause of an interrupted context, and the error of a dialog
// the user left with Ctrl-C. errors.Is(ErrInterrupted, context.Canceled) is true, so it is handled
// as any other cancellation.
var ErrInterrupted error = interruptedError{}

type interruptedError struct{}

func (interruptedError) Error() string { return "interrupted" }

func (interruptedError) Is(target error) bool { return target == context.Canceled }

// source is the interrupt state of one context.
type source struct {
	cancel context.CancelCauseFunc
	notice func() // Tells the user about the interrupt

	once     sync.Once
	mu       sync.Mutex
	holds    int  // Unreleased HoldNotice calls
	heldBack bool // The notice waits for the holds to be released
}

type sourceKey struct{}

// WithCancel returns a context that Trigger cancels, with ErrInterrupted as the cause. notice, if
// not nil, is called once on the interrupt to tell the user about it.
func WithCancel(parent context.Context, notice func()) context.Context {
	ctx, _ := withSource(parent, notice)
	return ctx
}

func withSource(parent context.Context, notice func()) (context.Context, *source) {
	ctx, cancel := context.WithCancelCause(parent)
	s := &source{cancel: cancel, notice: notice}
	return context.WithValue(ctx, sourceKey{}, s), s
}

// ExitCode is the exit code of an interrupted command, 128 + SIGINT.
const ExitCode = 130

// forceExit ends the process on a second interrupt, sig. Tests replace it.
var forceExit = exitOnSignal

// NotifyContext returns a context that is cancelled on SIGINT, SIGTERM and Trigger. A signal after
// that interrupt terminates the process at once, see exitOnSignal. stop releases the signal
// handler.
func NotifyContext(parent context.Context, notice func()) (ctx context.Context, stop func()) {
	ctx, s := withSource(parent, notice)
	signals := make(chan os.Signal, 1)
	stopped, release := context.WithCancel(context.Background())
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	interrupted := func() bool { return context.Cause(ctx) == ErrInterrupted } //nolint:errorlint // The exact cause
	go func() {
		// Wait for the first interrupt: a signal, or a Trigger call. When several cases are ready,
		// select picks one at random, so the signal case checks what else has happened.
		select {
		case sig := <-signals:
			if stopped.Err() != nil {
				return // The command has finished
			}
			if interrupted() {
				forceExit(sig) // A Trigger call was the first interrupt
				return
			}
			s.trigger()
		case <-ctx.Done():
			if !interrupted() {
				// Cancelled by stop or by the parent: leave the signals to Go's default handler
				signal.Stop(signals)
				return
			}
		}
		// The second interrupt force-quits.
		select {
		case sig := <-signals:
			if stopped.Err() == nil {
				forceExit(sig)
			}
		case <-stopped.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		release()
		s.cancel(nil)
	}
}

// Trigger interrupts the command that ctx belongs to. It does nothing if ctx was not made by this
// package, or was interrupted already.
func Trigger(ctx context.Context) {
	if s, ok := ctx.Value(sourceKey{}).(*source); ok {
		s.trigger()
	}
}

func (s *source) trigger() {
	s.once.Do(func() {
		s.mu.Lock()
		if s.holds > 0 {
			s.heldBack = true
		} else if s.notice != nil {
			s.notice()
		}
		s.mu.Unlock()

		s.cancel(ErrInterrupted)
	})
}

// HoldNotice holds the interrupt notice back until release is called. A TUI holds it while its
// display is on screen, since text written over the display garbles it.
func HoldNotice(ctx context.Context) (release func()) {
	s, ok := ctx.Value(sourceKey{}).(*source)
	if !ok {
		return func() {}
	}
	s.mu.Lock()
	s.holds++
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.holds--
			if s.holds == 0 && s.heldBack {
				s.heldBack = false
				if s.notice != nil {
					s.notice()
				}
			}
		})
	}
}
