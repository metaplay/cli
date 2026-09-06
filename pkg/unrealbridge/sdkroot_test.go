/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSdkRoot(t *testing.T) {
	t.Run("valid root", func(t *testing.T) {
		root := writeFakeSdkRoot(t)
		if err := ValidateSdkRoot(root); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing tools", func(t *testing.T) {
		if err := ValidateSdkRoot(t.TempDir()); err == nil {
			t.Error("expected an error for a directory without the SDK tools")
		}
	})

	t.Run("empty root", func(t *testing.T) {
		if err := ValidateSdkRoot(""); err == nil {
			t.Error("expected an error for an empty root")
		}
	})
}

func TestParseSdkRootDirFromProjectConfig(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"plain value", "sdkRootDir: MetaplaySDK\n", "MetaplaySDK"},
		{"relative path", "projectID: foo-bar\nsdkRootDir: ../sdks/MetaplaySDK\n", "../sdks/MetaplaySDK"},
		{"quoted", `sdkRootDir: "MetaplaySDK"`, "MetaplaySDK"},
		{"not present", "projectID: foo-bar\n", ""},
		{"other keys only", "buildRootDir: .\nbackendDir: Backend\n", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseSdkRootDirFromProjectConfig([]byte(test.yaml)); got != test.want {
				t.Errorf("parseSdkRootDirFromProjectConfig = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveSdkRootFromProjectConfig(t *testing.T) {
	t.Run("walks up to the config", func(t *testing.T) {
		root := t.TempDir()
		sdkRoot := writeFakeSdkRoot(t)

		// Place metaplay-project.yaml two levels above the project dir.
		configDir := filepath.Join(root, "game")
		projectDir := filepath.Join(configDir, "Unreal")
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("failed to create dirs: %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "metaplay-project.yaml"), []byte("sdkRootDir: "+sdkRoot+"\n"), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		got, err := resolveSdkRootFromProjectConfig(projectDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sdkRoot {
			t.Errorf("resolveSdkRootFromProjectConfig = %q, want %q", got, sdkRoot)
		}
	})

	t.Run("invalid sdkRootDir surfaces the validation reason", func(t *testing.T) {
		root := t.TempDir()
		projectDir := filepath.Join(root, "game", "Unreal")
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("failed to create dirs: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "metaplay-project.yaml"), []byte("sdkRootDir: NoSuchSdkDir\n"), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		_, err := resolveSdkRootFromProjectConfig(projectDir)
		if err == nil {
			t.Fatal("expected an error for an invalid sdkRootDir")
		}
		if !strings.Contains(err.Error(), "does not point to a valid MetaplaySDK root") {
			t.Errorf("error lacks the sdkRootDir context: %v", err)
		}
		// The specific validation failure (what is missing under the directory) must
		// be included, not just a generic statement.
		if !strings.Contains(err.Error(), "the SDK tools were not found under") {
			t.Errorf("error lacks the underlying validation reason: %v", err)
		}
	})

	t.Run("no config anywhere", func(t *testing.T) {
		if _, err := resolveSdkRootFromProjectConfig(t.TempDir()); err == nil {
			t.Error("expected an error when no metaplay-project.yaml exists upwards")
		}
	})
}

func TestResolveSdkRoot(t *testing.T) {
	t.Run("flag override wins", func(t *testing.T) {
		sdkRoot := writeFakeSdkRoot(t)
		doc, err := ParseCsProj([]byte(`<Project></Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := resolveSdkRoot(sdkRoot, "some/csproj.csproj", doc, t.TempDir())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sdkRoot {
			t.Errorf("resolveSdkRoot = %q, want %q", got, sdkRoot)
		}
	})

	t.Run("invalid override fails", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<Project></Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := resolveSdkRoot(t.TempDir(), "csproj.csproj", doc, t.TempDir()); err == nil {
			t.Error("expected an error for an override that is not an SDK root")
		}
	})

	t.Run("derived from the shared recipe import", func(t *testing.T) {
		sdkRoot := writeFakeSdkRoot(t)

		// The recipe file itself must exist at the expected location inside the SDK.
		bridgeBuildDir := filepath.Join(sdkRoot, "Unreal", "MetaplayUnreal", "BridgeBuild")
		if err := os.MkdirAll(bridgeBuildDir, 0755); err != nil {
			t.Fatalf("failed to create BridgeBuild dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeBuildDir, "MetaplayUnreal.BridgeHost.props"), []byte("<Project/>"), 0644); err != nil {
			t.Fatalf("failed to write props: %v", err)
		}

		// A bridge project living OUTSIDE the SDK tree, importing the recipe by a
		// relative path (the HelloUnreal sample layout).
		gameRoot := t.TempDir()
		bridgeDir := filepath.Join(gameRoot, "Bridge")
		if err := os.MkdirAll(bridgeDir, 0755); err != nil {
			t.Fatalf("failed to create Bridge dir: %v", err)
		}
		relImport, err := filepath.Rel(bridgeDir, filepath.Join(bridgeBuildDir, "MetaplayUnreal.BridgeHost.props"))
		if err != nil {
			t.Fatalf("failed to compute relative import: %v", err)
		}
		csprojPath := filepath.Join(bridgeDir, "MyGame.Bridge.csproj")
		doc, err := ParseCsProj([]byte(`<Project Sdk="Microsoft.NET.Sdk">
			<Import Project="` + filepath.ToSlash(relImport) + `" />
		</Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := resolveSdkRoot("", csprojPath, doc, gameRoot)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sdkRoot {
			t.Errorf("resolveSdkRoot = %q, want %q", got, sdkRoot)
		}
	})

	t.Run("derived from the shared recipe import with Windows-style separators", func(t *testing.T) {
		// A csproj authored with backslash separators must resolve on any host
		// (MSBuild treats them as separators on all platforms).
		sdkRoot := writeFakeSdkRoot(t)
		gameRoot := t.TempDir()
		bridgeDir := filepath.Join(gameRoot, "Bridge")
		if err := os.MkdirAll(bridgeDir, 0755); err != nil {
			t.Fatalf("failed to create Bridge dir: %v", err)
		}

		propsPath := filepath.Join(sdkRoot, "Unreal", "MetaplayUnreal", "BridgeBuild", "MetaplayUnreal.BridgeHost.props")
		relImport, err := filepath.Rel(bridgeDir, propsPath)
		if err != nil {
			t.Fatalf("failed to compute relative import: %v", err)
		}
		windowsImport := strings.ReplaceAll(relImport, string(filepath.Separator), `\`)

		csprojPath := filepath.Join(bridgeDir, "MyGame.Bridge.csproj")
		doc, err := ParseCsProj([]byte(`<Project Sdk="Microsoft.NET.Sdk">
			<Import Project="` + windowsImport + `" />
		</Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := resolveSdkRoot("", csprojPath, doc, gameRoot)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sdkRoot {
			t.Errorf("resolveSdkRoot = %q, want %q", got, sdkRoot)
		}
	})
}
