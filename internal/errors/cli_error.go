/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package errors

import (
	"errors"
	"fmt"
)

// ExitCode represents the type of error for exit code determination.
type ExitCode int

const (
	ExitRuntime ExitCode = 1 // Runtime/execution errors
	ExitUsage   ExitCode = 2 // Usage/argument errors
)

// CLIError is a user-friendly error with optional suggestion and details.
// It wraps an underlying Go error while providing a clean message for users.
type CLIError struct {
	Message    string   // User-friendly message (shown prominently)
	Cause      error    // Underlying Go error (shown dimmed)
	Suggestion string   // "Hint: ..." actionable suggestion for fixing the error
	Details    []string // Extra bullet points with additional context
	Code       ExitCode // Exit code (1=runtime, 2=usage)
}

// Error implements the error interface.
func (e *CLIError) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause for errors.Is/As support.
func (e *CLIError) Unwrap() error {
	return e.Cause
}

// IsUsageError returns true if this is a usage/argument error.
func (e *CLIError) IsUsageError() bool {
	return e.Code == ExitUsage
}

// WithSuggestion adds a suggestion hint to the error.
func (e *CLIError) WithSuggestion(suggestion string) *CLIError {
	e.Suggestion = suggestion
	return e
}

// WithDetails adds detail bullet points to the error.
func (e *CLIError) WithDetails(details ...string) *CLIError {
	e.Details = append(e.Details, details...)
	return e
}

// WithCause sets the underlying cause error.
func (e *CLIError) WithCause(cause error) *CLIError {
	e.Cause = cause
	return e
}

// New creates a new CLIError with a user-friendly message.
func New(message string) *CLIError {
	return &CLIError{
		Message: message,
		Code:    ExitRuntime,
	}
}

// Newf creates a new CLIError with a formatted user-friendly message.
func Newf(format string, args ...any) *CLIError {
	return &CLIError{
		Message: fmt.Sprintf(format, args...),
		Code:    ExitRuntime,
	}
}

// Wrap wraps an existing error with a user-friendly message.
func Wrap(cause error, message string) *CLIError {
	return &CLIError{
		Message: message,
		Cause:   cause,
		Code:    ExitRuntime,
	}
}

// Wrapf wraps an existing error with a formatted user-friendly message.
func Wrapf(cause error, format string, args ...any) *CLIError {
	return &CLIError{
		Message: fmt.Sprintf(format, args...),
		Cause:   cause,
		Code:    ExitRuntime,
	}
}

// NewUsageError creates a new usage/argument error.
// Usage errors cause the command's usage help to be shown.
func NewUsageError(message string) *CLIError {
	return &CLIError{
		Message: message,
		Code:    ExitUsage,
	}
}

// NewUsageErrorf creates a new usage/argument error with formatting.
func NewUsageErrorf(format string, args ...any) *CLIError {
	return &CLIError{
		Message: fmt.Sprintf(format, args...),
		Code:    ExitUsage,
	}
}

// WrapUsageError wraps an existing error as a usage error.
func WrapUsageError(cause error, message string) *CLIError {
	return &CLIError{
		Message: message,
		Cause:   cause,
		Code:    ExitUsage,
	}
}

// IsUsageError checks if an error is a usage error (should show usage help).
func IsUsageError(err error) bool {
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return cliErr.IsUsageError()
	}
	return false
}

// GetExitCode returns the appropriate exit code for an error.
func GetExitCode(err error) int {
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return int(cliErr.Code)
	}
	return int(ExitRuntime) // Default to runtime error
}

// AsCLIError attempts to extract a CLIError from an error chain.
func AsCLIError(err error) (*CLIError, bool) {
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return cliErr, true
	}
	return nil, false
}
