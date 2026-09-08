/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateMirrorHeader(t *testing.T) {
	writeGenerated := func(content string) func(string) error {
		return func(tmpPath string) error {
			return os.WriteFile(tmpPath, []byte(content), 0644)
		}
	}

	t.Run("writes a new header", func(t *testing.T) {
		projectDir := t.TempDir()
		updated, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", writeGenerated("// header v1\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Error("expected updated=true for a new header")
		}
		content, err := os.ReadFile(filepath.Join(projectDir, "Source", "MyGame", "Public", "MetaplayBridgeModels.h"))
		if err != nil {
			t.Fatalf("failed to read the header: %v", err)
		}
		if string(content) != "// header v1\n" {
			t.Errorf("header content = %q", content)
		}
	})

	t.Run("unchanged header is not rewritten", func(t *testing.T) {
		projectDir := t.TempDir()
		headerPath := filepath.Join(projectDir, "Source", "MyGame", "Public", "MetaplayBridgeModels.h")
		if _, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", writeGenerated("// header v1\n")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		before, err := os.Stat(headerPath)
		if err != nil {
			t.Fatalf("failed to stat: %v", err)
		}

		updated, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", writeGenerated("// header v1\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated {
			t.Error("expected updated=false for an unchanged header")
		}

		after, err := os.Stat(headerPath)
		if err != nil {
			t.Fatalf("failed to stat: %v", err)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Error("an unchanged header must not be rewritten (it would recompile the game module)")
		}

		// The temp directory must be cleaned up.
		if _, err := os.Stat(filepath.Join(projectDir, ".metaplay-mirrorgen.tmp")); !os.IsNotExist(err) {
			t.Error("the mirror temp directory was not removed")
		}
	})

	t.Run("changed header is updated", func(t *testing.T) {
		projectDir := t.TempDir()
		if _, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", writeGenerated("// header v1\n")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updated, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", writeGenerated("// header v2\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Error("expected updated=true for a changed header")
		}
		content, err := os.ReadFile(filepath.Join(projectDir, "Source", "MyGame", "Public", "MetaplayBridgeModels.h"))
		if err != nil {
			t.Fatalf("failed to read the header: %v", err)
		}
		if string(content) != "// header v2\n" {
			t.Errorf("header content = %q", content)
		}
	})

	t.Run("generator failure propagates", func(t *testing.T) {
		projectDir := t.TempDir()
		_, err := UpdateMirrorHeader(projectDir, "Source/MyGame/Public/MetaplayBridgeModels.h", func(tmpPath string) error {
			return os.ErrPermission
		})
		if err == nil {
			t.Error("expected the generator error to propagate")
		}
	})
}
