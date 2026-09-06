/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// UpdateMirrorHeader regenerates the USTRUCT mirror header of the game's Unreal module
// (only called when the project declares where it goes). The generator writes to a
// temporary copy that keeps the exact final file name (the generator derives the
// <name>.generated.h include from the output file's base name), and the header is
// diff-copied so an unchanged header does not recompile the game module.
//
// generate is invoked with the temporary output path and is expected to produce the
// header there. Returns true when the destination header was (re)written.
func UpdateMirrorHeader(projectDir string, mirrorHeaderRel string, generate func(tmpPath string) error) (bool, error) {
	mirrorHeaderPath := filepath.Join(projectDir, filepath.FromSlash(mirrorHeaderRel))
	tmpDir := filepath.Join(projectDir, ".metaplay-mirrorgen.tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create mirror temp directory %s: %w", tmpDir, err)
	}
	tmpPath := filepath.Join(tmpDir, filepath.Base(mirrorHeaderPath))
	if err := generate(tmpPath); err != nil {
		return false, err
	}

	// The header's directory may not exist yet in a clean checkout (git keeps no empty
	// directories; a game module without any public header has no Public/).
	if err := os.MkdirAll(filepath.Dir(mirrorHeaderPath), 0755); err != nil {
		return false, fmt.Errorf("failed to create mirror header directory: %w", err)
	}

	existing, readErr := os.ReadFile(mirrorHeaderPath)
	generated, err := os.ReadFile(tmpPath)
	if err != nil {
		return false, fmt.Errorf("failed to read generated mirror header %s: %w", tmpPath, err)
	}
	if readErr == nil && bytes.Equal(existing, generated) {
		// Unchanged header: remove the temporary copy so the game module does not recompile.
		if err := os.Remove(tmpPath); err != nil {
			return false, fmt.Errorf("failed to remove temporary mirror header %s: %w", tmpPath, err)
		}
		_ = os.Remove(tmpDir)
		return false, nil
	}

	if err := os.Rename(tmpPath, mirrorHeaderPath); err != nil {
		return false, fmt.Errorf("failed to move mirror header into place: %w", err)
	}
	_ = os.Remove(tmpDir)
	return true, nil
}
