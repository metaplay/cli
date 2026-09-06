/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockTimeout matches the SDK's metaplay-bridge pre-build step: one chain at a time
// per project, waiting up to 30 minutes for a concurrent chain (a single Unreal build
// invocation regenerates several targets' makefiles in parallel, each running the
// pre-build step against the same .NET projects).
const lockTimeout = 30 * time.Minute

// AcquireProjectLock takes the per-project bridge build lock under the project's
// Intermediate directory. It returns a release function that must be called when the
// build chain finishes.
//
// The lock file path matches metaplay-bridge.sh exactly, so the CLI command and the
// SDK's shell pre-build step exclude each other when both are in use.
//
// Child processes do not inherit the lock: Go opens files with close-on-exec, so
// MSBuild's daemonized worker nodes (nodeReuse:true) cannot hold the lock past the
// chain's lifetime (the reason metaplay-bridge.sh closes fd 9 for its children).
func AcquireProjectLock(projectDir string) (func(), error) {
	lockDir := filepath.Join(projectDir, "Intermediate", "Build")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory %s: %w", lockDir, err)
	}
	lockPath := filepath.Join(lockDir, "metaplay-bridge.lock")
	return lockFileExclusive(lockPath, lockTimeout)
}
