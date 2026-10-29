/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLiteralMsbuildProperty(t *testing.T) {
	testCases := []struct {
		name    string
		props   string
		want    string
		wantErr bool
	}{
		{name: "literal", props: `<Project><PropertyGroup><MetaplaySDKVersion> 39.0.0 </MetaplaySDKVersion></PropertyGroup></Project>`, want: "39.0.0"},
		{name: "among other groups", props: `<Project><PropertyGroup Condition="'$(X)' == ''"><Other>1</Other></PropertyGroup><PropertyGroup><MetaplaySDKVersion>39.1.0-pre</MetaplaySDKVersion></PropertyGroup></Project>`, want: "39.1.0-pre"},
		{name: "missing", props: `<Project><PropertyGroup><Other>1</Other></PropertyGroup></Project>`, wantErr: true},
		{name: "twice", props: `<Project><PropertyGroup><MetaplaySDKVersion>1.0.0</MetaplaySDKVersion><MetaplaySDKVersion>2.0.0</MetaplaySDKVersion></PropertyGroup></Project>`, wantErr: true},
		{name: "expression", props: `<Project><PropertyGroup><MetaplaySDKVersion>$(Version)</MetaplaySDKVersion></PropertyGroup></Project>`, wantErr: true},
		{name: "conditional group", props: `<Project><PropertyGroup Condition="'$(X)' == ''"><MetaplaySDKVersion>1.0.0</MetaplaySDKVersion></PropertyGroup></Project>`, wantErr: true},
		{name: "conditional property", props: `<Project><PropertyGroup><MetaplaySDKVersion Condition="true">1.0.0</MetaplaySDKVersion></PropertyGroup></Project>`, wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Directory.Build.props")
			if err := os.WriteFile(path, []byte(tc.props), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readLiteralMsbuildProperty(path, sdkVersionProperty)
			if tc.wantErr {
				if err == nil {
					t.Errorf("got %q, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// The SDK files come from the project's local package source, extracted into the CLI's cache, before
// the NuGet package cache.
func TestResolveSdkPackageDir_LocalPackageSource(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NUGET_PACKAGES", filepath.Join(t.TempDir(), "nuget"))

	props := `<Project><PropertyGroup><MetaplaySDKVersion>39.0.0-pre</MetaplaySDKVersion></PropertyGroup></Project>`
	if err := os.WriteFile("Directory.Build.props", []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("NuGetPackages", 0o755); err != nil {
		t.Fatal(err)
	}
	writeZip(t, filepath.Join("NuGetPackages", "Metaplay.Server.39.0.0-pre.nupkg"), map[string]string{
		"lib/net10.0/Metaplay.Server.dll": "dll",
		"sdk/version.yaml":                "sdkVersion: 39.0.0-pre\n",
		"sdk/entrypoint/output/a":         "a",
	})

	project, err := NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := project.ResolveSdkDir()
	if err != nil {
		t.Fatalf("ResolveSdkDir() error: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "entrypoint", "output", "a")); err != nil || string(content) != "a" {
		t.Errorf("extracted file: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "lib")); err == nil {
		t.Errorf("extracted more than sdk/")
	}
}

// Without a local package source, the SDK files come from the NuGet package cache, once NuGet has
// finished extracting the package there.
func TestResolveSdkPackageDir_NugetCache(t *testing.T) {
	t.Chdir(t.TempDir())
	nugetDir := filepath.Join(t.TempDir(), "nuget")
	t.Setenv("NUGET_PACKAGES", nugetDir)
	props := `<Project><PropertyGroup><MetaplaySDKVersion>39.0.0-PRE</MetaplaySDKVersion></PropertyGroup></Project>`
	if err := os.WriteFile("Directory.Build.props", []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(nugetDir, "metaplay.server", "39.0.0-pre")
	if err := os.MkdirAll(filepath.Join(packageDir, "sdk"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Not finished: no .nupkg.metadata yet
	project, err := NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if dir, err := project.ResolveSdkDir(); err == nil {
		t.Errorf("ResolveSdkDir() = %q before NuGet finished, want an error", dir)
	}

	if err := os.WriteFile(filepath.Join(packageDir, ".nupkg.metadata"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err = NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := project.ResolveSdkDir()
	if err != nil || dir != filepath.Join(packageDir, "sdk") {
		t.Errorf("ResolveSdkDir() = %q, %v, want %q", dir, err, filepath.Join(packageDir, "sdk"))
	}
}

// A project without sdkRootDir whose MetaplaySDK/ has the SDK's sources would build from the sources
// while the CLI and the server image use the packages, so the CLI refuses it.
func TestResolveSdkPackageDir_SourcesPresent(t *testing.T) {
	t.Chdir(t.TempDir())
	props := `<Project><PropertyGroup><MetaplaySDKVersion>39.0.0</MetaplaySDKVersion></PropertyGroup></Project>`
	if err := os.WriteFile("Directory.Build.props", []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}
	cloudDir := filepath.Join("MetaplaySDK", "Backend", "Cloud")
	if err := os.MkdirAll(cloudDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloudDir, "Metaplay.Cloud.csproj"), []byte("<Project />"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if dir, err := project.ResolveSdkDir(); err == nil || !strings.Contains(err.Error(), "MetaplaySDK/ has the SDK's sources") {
		t.Errorf("ResolveSdkDir() = %q, %v, want the error about the SDK's sources", dir, err)
	}
}

func TestRequireSdkSources(t *testing.T) {
	packages, err := NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := packages.RequireSdkSources("test system"); err == nil {
		t.Errorf("RequireSdkSources() = nil for a project with the SDK as packages")
	}
	sources, err := NewMetaplayProject(".", &ProjectConfig{SdkRootDir: "MetaplaySDK"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.RequireSdkSources("test system"); err != nil {
		t.Errorf("RequireSdkSources() = %v for a project with sdkRootDir", err)
	}
}

func TestResolveSdkPackageDir_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("NUGET_PACKAGES", filepath.Join(t.TempDir(), "nuget"))
	props := `<Project><PropertyGroup><MetaplaySDKVersion>39.0.0</MetaplaySDKVersion></PropertyGroup></Project>`
	if err := os.WriteFile("Directory.Build.props", []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := NewMetaplayProject(".", &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if dir, err := project.ResolveSdkDir(); err == nil {
		t.Errorf("ResolveSdkDir() = %q, want an error", dir)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(out)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
