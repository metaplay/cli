//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package pathutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForDisplayPassesPathsThrough(t *testing.T) {
	// Unix paths have no prefix to clean up, including ones that merely look like a Windows
	// device path.
	for _, path := range []string{
		"/usr/local/bin/metaplay",
		"/opt/homebrew/Cellar/metaplay/1.12.0/bin/metaplay",
		`\\?\weird-but-valid-unix-filename`,
	} {
		assert.Equal(t, path, ForDisplay(path))
	}
}
