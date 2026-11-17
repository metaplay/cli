//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

// checkReplaceable is a no-op on Unix: renaming a file requires write permission on the
// containing directory rather than on the file itself, which EnsureReplaceable already probes
// by creating a file there.
//
// That covers the common cases (an unwritable or read-only install directory) but not all of
// them: a sticky-bit directory additionally requires owning the file, and an immutable file
// (chattr +i, chflags schg) cannot be renamed at all. Those still fail in update.Apply, where
// the EPERM surfaces as a permission error and draws the same hint — just after the download
// rather than before it.
func checkReplaceable(_ string) error {
	return nil
}
