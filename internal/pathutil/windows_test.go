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
		{
			name:     "handles lower-case UNC prefixes",
			path:     `\\?\unc\server\share\metaplay.exe`,
			expected: `\\server\share\metaplay.exe`,
		},
		{
			// Stripping this would yield something that reads as a relative path and is not
			// usable anywhere, so it must be left as-is.
			name:     "leaves volume GUID paths intact",
			path:     `\\?\Volume{b75e2c83-0000-0000-0000-602f00000000}\metaplay.exe`,
			expected: `\\?\Volume{b75e2c83-0000-0000-0000-602f00000000}\metaplay.exe`,
		},
		{
			name:     "leaves GLOBALROOT paths intact",
			path:     `\\?\GLOBALROOT\Device\HarddiskVolume1\metaplay.exe`,
			expected: `\\?\GLOBALROOT\Device\HarddiskVolume1\metaplay.exe`,
		},
		{
			name:     "leaves a bare prefix alone",
			path:     `\\?\`,
			expected: `\\?\`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ForDisplay(tt.path))
		})
	}
}
