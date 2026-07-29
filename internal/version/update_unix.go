//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

// checkReplaceable is a no-op on Unix: renaming a file requires write permission on the
// containing directory rather than on the file itself, which CheckWritable already probes
// by creating a file there.
func checkReplaceable(_ string) error {
	return nil
}
