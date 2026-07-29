//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package pathutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForDisplay(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "strips the extended-length prefix",
			path:     `\\?\C:\ProgramData\chocolatey\lib\metaplay\tools\metaplay.exe`,
			expected: `C:\ProgramData\chocolatey\lib\metaplay\tools\metaplay.exe`,
		},
		{
			name:     "restores UNC paths to their familiar form",
			path:     `\\?\UNC\server\share\tools\metaplay.exe`,
			expected: `\\server\share\tools\metaplay.exe`,
		},
		{
			name:     "leaves an unprefixed path alone",
			path:     `C:\Users\dev\metaplay.exe`,
			expected: `C:\Users\dev\metaplay.exe`,
		},
		{
			name:     "leaves a plain UNC path alone",
			path:     `\\server\share\metaplay.exe`,
			expected: `\\server\share\metaplay.exe`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ForDisplay(tt.path))
		})
	}
}
