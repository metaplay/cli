/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/metaplay/cli/internal/version"
)

// embeddedData carries the canonical skill payload built into the CLI binary.
// Release builds always read through this filesystem.
//
//go:embed all:data
var embeddedData embed.FS

// OpenFS returns the filesystem rooted at the skills data directory.
//
// In dev builds (go run, unstamped go build) we prefer reading directly from
// internal/skills/data on disk so authors can iterate on skill content without
// rebuilding the CLI. The disk path is derived from runtime.Caller, which
// holds the source path of this file as long as the binary was not built with
// -trimpath. If the disk lookup fails for any reason we fall back to the
// embedded copy, which always works.
func OpenFS() fs.FS {
	if version.IsDevBuild() {
		if disk, ok := devDataDir(); ok {
			return os.DirFS(disk)
		}
	}
	sub, err := fs.Sub(embeddedData, "data")
	if err != nil {
		// fs.Sub on a known prefix only fails for invalid paths, which
		// "data" is not — keep the panic to surface programmer error.
		panic(err)
	}
	return sub
}

// devDataDir returns the on-disk path to internal/skills/data when the binary
// is run from the source tree, plus an existence flag.
func devDataDir() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	dir := filepath.Join(filepath.Dir(file), "data")
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", false
	}
	return dir, true
}
