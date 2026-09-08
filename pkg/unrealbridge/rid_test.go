/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimeIdentifier(t *testing.T) {
	tests := []struct {
		platform string
		goos     string
		goarch   string
		wantRid  string
		wantErr  bool
	}{
		{"Linux", "linux", "amd64", "linux-x64", false},
		{"Linux", "linux", "arm64", "linux-arm64", false},
		{"Linux", "linux", "386", "", true},
		{"Linux", "darwin", "arm64", "", true},
		{"Linux", "windows", "amd64", "", true},
		{"Win64", "windows", "amd64", "win-x64", false},
		{"Win64", "windows", "arm64", "win-x64", false},
		{"Win64", "linux", "amd64", "", true},
		{"Mac", "darwin", "arm64", "osx-arm64", false},
		{"Mac", "darwin", "amd64", "osx-x64", false},
		{"Mac", "linux", "amd64", "", true},
		// Not a desktop platform: skip, not an error.
		{"IOS", "linux", "amd64", "", false},
		{"Android", "linux", "arm64", "", false},
		{"TVOS", "darwin", "arm64", "", false},
	}
	for _, test := range tests {
		t.Run(test.platform+"/"+test.goos+"/"+test.goarch, func(t *testing.T) {
			rid, err := ResolveRuntimeIdentifier(test.platform, test.goos, test.goarch)
			if test.wantErr {
				if err == nil {
					t.Errorf("expected an error, got rid %q", rid)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rid != test.wantRid {
				t.Errorf("rid = %q, want %q", rid, test.wantRid)
			}
		})
	}
}

func TestNativeLibraryGlob(t *testing.T) {
	tests := []struct {
		rid  string
		want string
	}{
		{"linux-x64", "*.so"},
		{"linux-arm64", "*.so"},
		{"win-x64", "*.dll"},
		{"osx-arm64", "*.dylib"},
		{"osx-x64", "*.dylib"},
	}
	for _, test := range tests {
		t.Run(test.rid, func(t *testing.T) {
			if got := NativeLibraryGlob(test.rid); got != test.want {
				t.Errorf("NativeLibraryGlob(%q) = %q, want %q", test.rid, got, test.want)
			}
		})
	}
}

func TestTargetTypeConsumesBridge(t *testing.T) {
	tests := []struct {
		targetType string
		want       bool
	}{
		{"Game", true},
		{"Editor", true},
		{"Client", true},
		{"Server", false},
		{"Program", false},
	}
	for _, test := range tests {
		t.Run(test.targetType, func(t *testing.T) {
			if got := TargetTypeConsumesBridge(test.targetType); got != test.want {
				t.Errorf("TargetTypeConsumesBridge(%q) = %v, want %v", test.targetType, got, test.want)
			}
		})
	}
}

// writeFakeSdkRoot writes a directory structure that satisfies ValidateSdkRoot and
// returns its path (including the shared bridge host recipe file).
func writeFakeSdkRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join("Dotnet", "SerializerGen"),
		filepath.Join("Dotnet", "MirrorGen"),
		filepath.Join("Unreal", "MetaplayUnreal", "BridgeBuild"),
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}
	propsPath := filepath.Join(root, "Unreal", "MetaplayUnreal", "BridgeBuild", "MetaplayUnreal.BridgeHost.props")
	if err := os.WriteFile(propsPath, []byte("<Project/>"), 0644); err != nil {
		t.Fatalf("failed to write props: %v", err)
	}
	return root
}

func TestHostRuntimeIdentifier(t *testing.T) {
	rid, err := HostRuntimeIdentifier()
	if err != nil {
		// Only expected to fail on non-desktop hosts.
		t.Skipf("host has no runtime identifier: %v", err)
	}
	if rid == "" {
		t.Error("expected a non-empty runtime identifier on a desktop host")
	}
}
