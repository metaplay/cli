/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"testing"

	"github.com/metaplay/cli/internal/version"
	"github.com/stretchr/testify/assert"
)

func TestReplaceFailureErrorPicksHintFromCause(t *testing.T) {
	const exe = "/opt/metaplay/metaplay"

	tests := []struct {
		name           string
		err            error
		wantSuggestion string
		wantDetails    bool
	}{
		{
			name:           "an unwritable install points at the package manager",
			err:            fmt.Errorf("cannot replace %s: %w", exe, fs.ErrPermission),
			wantSuggestion: notWritableSuggestion,
			wantDetails:    true,
		},
		{
			// The case that matters most: on Windows a busy leftover reports
			// ERROR_ACCESS_DENIED, which satisfies errors.Is(fs.ErrPermission). It must still be
			// recognised as a busy file, or the user is told to elevate for a mapped image that
			// no privilege level can delete.
			name:           "a busy leftover wins over its permission errno",
			err:            fmt.Errorf("%w: %s: %w", version.ErrLeftoverInUse, exe, fs.ErrPermission),
			wantSuggestion: fileInUseSuggestion,
			wantDetails:    false,
		},
		{
			name:           "a busy leftover without a permission errno",
			err:            fmt.Errorf("%w: %s: sharing violation", version.ErrLeftoverInUse, exe),
			wantSuggestion: fileInUseSuggestion,
			wantDetails:    false,
		},
		{
			name:           "a missing executable falls back rather than blaming permissions",
			err:            fmt.Errorf("cannot replace %s: %w", exe, fs.ErrNotExist),
			wantSuggestion: manualInstallSuggestion,
			wantDetails:    false,
		},
		{
			name:           "an unclassified failure falls back",
			err:            errors.New("archive is corrupt"),
			wantSuggestion: manualInstallSuggestion,
			wantDetails:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliErr := replaceFailureError(tt.err, "Cannot replace the Metaplay CLI binary", exe, manualInstallSuggestion)

			assert.Equal(t, tt.wantSuggestion, cliErr.Suggestion)
			assert.Equal(t, "Cannot replace the Metaplay CLI binary", cliErr.Message)
			assert.ErrorIs(t, cliErr, tt.err, "the cause must stay in the chain")

			if tt.wantDetails {
				assert.NotEmpty(t, cliErr.Details, "a permission failure should list where to update from")
			} else {
				assert.Empty(t, cliErr.Details, "only a permission failure should list package managers")
			}
		})
	}
}

func TestReplaceFailureErrorUsesCallerFallback(t *testing.T) {
	// The download call site needs the network-flavoured hint, not the pre-flight's.
	cliErr := replaceFailureError(errors.New("unexpected status 500"),
		"Failed to update the Metaplay CLI binary", "/opt/metaplay/metaplay", manualDownloadSuggestion)

	assert.Equal(t, manualDownloadSuggestion, cliErr.Suggestion)
}

func TestNotWritableDetailsNamesInstallPathAndPlatformManagers(t *testing.T) {
	details := notWritableDetails("/opt/metaplay/metaplay")

	assert.Contains(t, details[0], "/opt/metaplay/metaplay", "the install path must be shown")

	joined := fmt.Sprint(details)
	if runtime.GOOS == "windows" {
		assert.Contains(t, joined, "choco upgrade metaplay")
		assert.Contains(t, joined, "scoop update metaplay")
		assert.NotContains(t, joined, "brew")
	} else {
		assert.Contains(t, joined, "brew upgrade metaplay")
		assert.NotContains(t, joined, "choco")
	}
}
