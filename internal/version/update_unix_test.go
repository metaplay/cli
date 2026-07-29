//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import "testing"

// lockFile reports that it could not lock: on Unix an open file is still unlinkable, so there
// is no equivalent of the Windows sharing violation to exercise.
func lockFile(_ *testing.T, _ string) (func(), bool) {
	return func() {}, false
}
