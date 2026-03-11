/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package errors

import (
	"errors"
	"fmt"
	"testing"
)

func TestError(t *testing.T) {
	err := New("something went wrong")
	if err.Error() != "something went wrong" {
		t.Errorf("expected 'something went wrong', got '%s'", err.Error())
	}
}

func TestNewf(t *testing.T) {
	err := Newf("missing file '%s'", "config.yaml")
	if err.Error() != "missing file 'config.yaml'" {
		t.Errorf("expected formatted message, got '%s'", err.Error())
	}
	if err.Code != ExitRuntime {
		t.Errorf("expected ExitRuntime, got %d", err.Code)
	}
}

func TestWrap(t *testing.T) {
	cause := fmt.Errorf("underlying issue")
	err := Wrap(cause, "operation failed")

	if err.Error() != "operation failed" {
		t.Errorf("expected 'operation failed', got '%s'", err.Error())
	}
	if err.Cause != cause {
		t.Error("expected cause to be set")
	}
}

func TestWrapf(t *testing.T) {
	cause := fmt.Errorf("timeout")
	err := Wrapf(cause, "failed to connect to %s", "server")

	if err.Error() != "failed to connect to server" {
		t.Errorf("expected formatted message, got '%s'", err.Error())
	}
	if err.Cause != cause {
		t.Error("expected cause to be set")
	}
}

func TestUnwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := Wrap(cause, "wrapper")

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Error("Unwrap should return the cause")
	}
}

func TestErrorsIs(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := Wrap(sentinel, "wrapped")

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is should find the sentinel error through CLIError")
	}
}

func TestErrorsAs(t *testing.T) {
	err := New("test error")
	var target *CLIError
	if !errors.As(err, &target) {
		t.Error("errors.As should extract CLIError")
	}
	if target.Message != "test error" {
		t.Errorf("expected 'test error', got '%s'", target.Message)
	}
}

func TestWithSuggestion(t *testing.T) {
	err := New("missing config").WithSuggestion("run 'init' first")
	if err.Suggestion != "run 'init' first" {
		t.Errorf("expected suggestion to be set, got '%s'", err.Suggestion)
	}
}

func TestWithDetails(t *testing.T) {
	err := New("build failed").WithDetails("step 1 failed", "step 2 skipped")
	if len(err.Details) != 2 {
		t.Errorf("expected 2 details, got %d", len(err.Details))
	}
	if err.Details[0] != "step 1 failed" {
		t.Errorf("expected first detail 'step 1 failed', got '%s'", err.Details[0])
	}
}

func TestWithCause(t *testing.T) {
	cause := fmt.Errorf("io error")
	err := New("read failed").WithCause(cause)
	if err.Cause != cause {
		t.Error("expected cause to be set via WithCause")
	}
}

func TestUsageError(t *testing.T) {
	err := NewUsageError("bad flag")
	if !err.IsUsageError() {
		t.Error("expected IsUsageError to be true")
	}
	if err.Code != ExitUsage {
		t.Errorf("expected ExitUsage, got %d", err.Code)
	}
}

func TestNewUsageErrorf(t *testing.T) {
	err := NewUsageErrorf("invalid flag '%s'", "--foo")
	if err.Error() != "invalid flag '--foo'" {
		t.Errorf("expected formatted message, got '%s'", err.Error())
	}
	if !err.IsUsageError() {
		t.Error("expected IsUsageError to be true")
	}
}

func TestWrapUsageError(t *testing.T) {
	cause := fmt.Errorf("parse error")
	err := WrapUsageError(cause, "invalid arguments")

	if err.Error() != "invalid arguments" {
		t.Errorf("expected 'invalid arguments', got '%s'", err.Error())
	}
	if !err.IsUsageError() {
		t.Error("expected IsUsageError to be true")
	}
	if err.Cause != cause {
		t.Error("expected cause to be set")
	}
}

func TestIsUsageErrorFunc(t *testing.T) {
	usageErr := NewUsageError("bad input")
	runtimeErr := New("internal failure")
	plainErr := fmt.Errorf("plain error")

	if !IsUsageError(usageErr) {
		t.Error("expected IsUsageError to return true for usage error")
	}
	if IsUsageError(runtimeErr) {
		t.Error("expected IsUsageError to return false for runtime error")
	}
	if IsUsageError(plainErr) {
		t.Error("expected IsUsageError to return false for plain error")
	}
}

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{"runtime error", New("fail"), 1},
		{"usage error", NewUsageError("bad flag"), 2},
		{"plain error defaults to runtime", fmt.Errorf("oops"), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := GetExitCode(tt.err)
			if code != tt.expected {
				t.Errorf("expected exit code %d, got %d", tt.expected, code)
			}
		})
	}
}

func TestAsCLIError(t *testing.T) {
	cliErr := New("test")
	plainErr := fmt.Errorf("plain")

	if result, ok := AsCLIError(cliErr); !ok || result == nil {
		t.Error("expected AsCLIError to find CLIError")
	}

	if _, ok := AsCLIError(plainErr); ok {
		t.Error("expected AsCLIError to return false for plain error")
	}
}

func TestRuntimeErrorIsNotUsageError(t *testing.T) {
	err := New("runtime problem")
	if err.IsUsageError() {
		t.Error("runtime error should not be a usage error")
	}
}

func TestBuilderChaining(t *testing.T) {
	cause := fmt.Errorf("root")
	err := New("problem").
		WithCause(cause).
		WithSuggestion("try again").
		WithDetails("detail 1", "detail 2")

	if err.Message != "problem" {
		t.Errorf("expected 'problem', got '%s'", err.Message)
	}
	if err.Cause != cause {
		t.Error("expected cause to be set")
	}
	if err.Suggestion != "try again" {
		t.Errorf("expected suggestion 'try again', got '%s'", err.Suggestion)
	}
	if len(err.Details) != 2 {
		t.Errorf("expected 2 details, got %d", len(err.Details))
	}
}
