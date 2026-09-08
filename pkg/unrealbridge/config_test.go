/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBridgeConfig(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		config, err := ParseBridgeConfig([]byte(`{
			"BridgeProject": "Bridge/MyGame.Bridge.csproj",
			"SerializerName": "MyGame",
			"Configuration": "Debug",
			"MirrorHeader": "Source/MyGame/Public/MetaplayBridgeModels.h",
			"StageDir": "Binaries/Custom"
		}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.BridgeProject != "Bridge/MyGame.Bridge.csproj" {
			t.Errorf("BridgeProject = %q", config.BridgeProject)
		}
		if config.SerializerName != "MyGame" {
			t.Errorf("SerializerName = %q", config.SerializerName)
		}
		if config.Configuration != "Debug" {
			t.Errorf("Configuration = %q", config.Configuration)
		}
		if config.MirrorHeader != "Source/MyGame/Public/MetaplayBridgeModels.h" {
			t.Errorf("MirrorHeader = %q", config.MirrorHeader)
		}
		if config.StageDir != "Binaries/Custom" {
			t.Errorf("StageDir = %q", config.StageDir)
		}
	})

	t.Run("empty config", func(t *testing.T) {
		config, err := ParseBridgeConfig([]byte(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.BridgeProject != "" {
			t.Errorf("BridgeProject = %q, want empty", config.BridgeProject)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseBridgeConfig([]byte(`{not json`))
		if err == nil {
			t.Error("expected an error for invalid JSON")
		}
	})
}

func TestParseCsProj(t *testing.T) {
	t.Run("sdk-style project", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<Project Sdk="Microsoft.NET.Sdk">
			<PropertyGroup>
				<AssemblyName>MyGame.Bridge</AssemblyName>
				<MetaplaySerializerName>MyGame</MetaplaySerializerName>
				<TargetFramework>net10.0</TargetFramework>
			</PropertyGroup>
			<ItemGroup>
				<ProjectReference Include="..\GameLogic\GameLogic.csproj" />
			</ItemGroup>
			<Import Project="..\..\..\MetaplaySDK\Unreal\MetaplayUnreal\BridgeBuild\MetaplayUnreal.BridgeHost.props" />
		</Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := doc.AssemblyName("SomeDir/Other.csproj"); got != "MyGame.Bridge" {
			t.Errorf("assemblyName = %q", got)
		}
		if got := doc.LastProperty(func(g CsprojPropertyGroup) string { return g.MetaplaySerializerName }); got != "MyGame" {
			t.Errorf("MetaplaySerializerName = %q", got)
		}
		if got := doc.LastProperty(func(g CsprojPropertyGroup) string { return g.TargetFramework }); got != "net10.0" {
			t.Errorf("TargetFramework = %q", got)
		}
		if got := doc.PropsImportPath(); got != `..\..\..\MetaplaySDK\Unreal\MetaplayUnreal\BridgeBuild\MetaplayUnreal.BridgeHost.props` {
			t.Errorf("propsImportPath = %q", got)
		}
	})

	t.Run("last one wins", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<Project>
			<PropertyGroup>
				<AssemblyName>First</AssemblyName>
			</PropertyGroup>
			<PropertyGroup>
				<AssemblyName>Second</AssemblyName>
				<MetaplaySerializerDirName>CustomSerializerDir</MetaplaySerializerDirName>
			</PropertyGroup>
		</Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := doc.AssemblyName("x.csproj"); got != "Second" {
			t.Errorf("assemblyName = %q, want Second (MSBuild last-one-wins)", got)
		}
		if got := doc.LastProperty(func(g CsprojPropertyGroup) string { return g.MetaplaySerializerDirName }); got != "CustomSerializerDir" {
			t.Errorf("MetaplaySerializerDirName = %q", got)
		}
	})

	t.Run("legacy format with xmlns", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<?xml version="1.0" encoding="utf-8"?>
		<Project xmlns="http://schemas.microsoft.com/developer/msbuild/2003" ToolsVersion="15.0">
			<PropertyGroup>
				<AssemblyName>LegacyGame</AssemblyName>
			</PropertyGroup>
		</Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := doc.AssemblyName("x.csproj"); got != "LegacyGame" {
			t.Errorf("assemblyName = %q", got)
		}
	})

	t.Run("assembly name falls back to file name", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<Project><PropertyGroup></PropertyGroup></Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := doc.AssemblyName("BridgeHost/BridgeHost.csproj"); got != "BridgeHost" {
			t.Errorf("assemblyName = %q, want BridgeHost", got)
		}
	})

	t.Run("no props import", func(t *testing.T) {
		doc, err := ParseCsProj([]byte(`<Project></Project>`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := doc.PropsImportPath(); got != "" {
			t.Errorf("propsImportPath = %q, want empty", got)
		}
	})
}

// writeBridgeProjectFixture writes an Unreal project directory fixture with a
// MetaplayBridge.json and a bridge host csproj importing the shared recipe from the
// given fake SDK root, returning the project dir. Pass an empty sdkRoot for a csproj
// whose import does not resolve.
func writeBridgeProjectFixture(t *testing.T, bridgeJSON string, sdkRoot string) string {
	t.Helper()
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "Bridge"), 0755); err != nil {
		t.Fatalf("failed to create Bridge dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, BridgeConfigFileName), []byte(bridgeJSON), 0644); err != nil {
		t.Fatalf("failed to write MetaplayBridge.json: %v", err)
	}

	importPath := `NOT_A_REAL_SDK\MetaplayUnreal\BridgeBuild\MetaplayUnreal.BridgeHost.props`
	if sdkRoot != "" {
		propsPath := filepath.Join(sdkRoot, "Unreal", "MetaplayUnreal", "BridgeBuild", propsFileName)
		rel, err := filepath.Rel(filepath.Join(projectDir, "Bridge"), propsPath)
		if err != nil {
			t.Fatalf("failed to compute relative import: %v", err)
		}
		importPath = filepath.ToSlash(rel)
	}

	csproj := `<Project Sdk="Microsoft.NET.Sdk">
	<PropertyGroup>
		<AssemblyName>MyGame.Bridge</AssemblyName>
		<MetaplaySerializerName>MyGame</MetaplaySerializerName>
	</PropertyGroup>
	<Import Project="` + importPath + `" />
</Project>`
	if err := os.WriteFile(filepath.Join(projectDir, "Bridge", "BridgeHost.csproj"), []byte(csproj), 0644); err != nil {
		t.Fatalf("failed to write csproj: %v", err)
	}
	return projectDir
}

func TestResolveBridgeSettings(t *testing.T) {
	t.Run("defaults and declared values", func(t *testing.T) {
		sdkRoot := writeFakeSdkRoot(t)
		projectDir := writeBridgeProjectFixture(t, `{
			"BridgeProject": "Bridge/BridgeHost.csproj",
			"SerializerName": "MyGame"
		}`, sdkRoot)

		settings, err := ResolveBridgeSettings(projectDir, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if settings.ProjectDir != projectDir {
			t.Errorf("ProjectDir = %q", settings.ProjectDir)
		}
		if settings.BridgeCsProjPath != filepath.Join(projectDir, "Bridge", "BridgeHost.csproj") {
			t.Errorf("BridgeCsProjPath = %q", settings.BridgeCsProjPath)
		}
		if settings.AssemblyName != "MyGame.Bridge" {
			t.Errorf("AssemblyName = %q", settings.AssemblyName)
		}
		if settings.SerializerName != "MyGame" {
			t.Errorf("SerializerName = %q", settings.SerializerName)
		}
		if settings.DotnetConfig != "Release" {
			t.Errorf("DotnetConfig = %q, want default Release", settings.DotnetConfig)
		}
		if settings.StageDirRel != DefaultStageDirRel {
			t.Errorf("StageDirRel = %q", settings.StageDirRel)
		}
		if settings.StageDir != filepath.Join(projectDir, filepath.FromSlash(DefaultStageDirRel)) {
			t.Errorf("StageDir = %q", settings.StageDir)
		}
		if settings.TargetFramework != "net10.0" {
			t.Errorf("TargetFramework = %q, want default net10.0", settings.TargetFramework)
		}
		if settings.SerializerDllPath != filepath.Join(settings.SerializerDir, "Metaplay.Generated.MyGame.dll") {
			t.Errorf("SerializerDllPath = %q", settings.SerializerDllPath)
		}
		if settings.RootAssemblyPath != filepath.Join(settings.BridgeDir, "bin", "Release", "net10.0", "MyGame.Bridge.dll") {
			t.Errorf("RootAssemblyPath = %q", settings.RootAssemblyPath)
		}
		if settings.SdkRoot != sdkRoot {
			t.Errorf("SdkRoot = %q, want %q (derived from the recipe import)", settings.SdkRoot, sdkRoot)
		}
		if settings.ExpectedHashPath() != filepath.Join(settings.StageDir, "expected.hash") {
			t.Errorf("ExpectedHashPath = %q", settings.ExpectedHashPath())
		}
	})

	t.Run("unresolvable SDK root fails with an error", func(t *testing.T) {
		projectDir := writeBridgeProjectFixture(t, `{"BridgeProject": "Bridge/BridgeHost.csproj"}`, "")
		if _, err := ResolveBridgeSettings(projectDir, "", ""); err == nil {
			t.Error("expected an error when the SDK root cannot be resolved")
		}
	})

	t.Run("missing MetaplayBridge.json", func(t *testing.T) {
		_, err := ResolveBridgeSettings(t.TempDir(), "", "")
		if err == nil {
			t.Error("expected an error when MetaplayBridge.json is missing")
		}
	})

	t.Run("missing BridgeProject declaration", func(t *testing.T) {
		projectDir := writeBridgeProjectFixture(t, `{}`, "")
		_, err := ResolveBridgeSettings(projectDir, "", "")
		if err == nil {
			t.Error("expected an error when BridgeProject is not declared")
		}
	})

	t.Run("bridge project file missing", func(t *testing.T) {
		projectDir := writeBridgeProjectFixture(t, `{"BridgeProject": "Bridge/Nope.csproj"}`, "")
		_, err := ResolveBridgeSettings(projectDir, "", "")
		if err == nil {
			t.Error("expected an error when the bridge csproj does not exist")
		}
	})

	t.Run("config and serializer name fallbacks via metaplay-project.yaml", func(t *testing.T) {
		// A nested layout: <root>/game/Unreal holds the project, <root> holds the
		// metaplay-project.yaml and the fake SDK.
		root := t.TempDir()
		sdkRoot := writeFakeSdkRoot(t)
		if err := os.Rename(sdkRoot, filepath.Join(root, "MetaplaySDK")); err != nil {
			t.Fatalf("failed to move the fake SDK: %v", err)
		}
		sdkRoot = filepath.Join(root, "MetaplaySDK")
		if err := os.WriteFile(filepath.Join(root, "metaplay-project.yaml"), []byte("sdkRootDir: MetaplaySDK\n"), 0644); err != nil {
			t.Fatalf("failed to write metaplay-project.yaml: %v", err)
		}

		// A project whose csproj has no MetaplaySerializerName and no resolvable recipe
		// import: the serializer name falls back to the assembly name and the SDK root
		// resolves from the metaplay-project.yaml found by walking up.
		projectDir := filepath.Join(root, "game", "Unreal")
		if err := os.MkdirAll(filepath.Join(projectDir, "Bridge"), 0755); err != nil {
			t.Fatalf("failed to create dirs: %v", err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, BridgeConfigFileName), []byte(`{"BridgeProject": "Bridge/BridgeHost.csproj"}`), 0644); err != nil {
			t.Fatalf("failed to write MetaplayBridge.json: %v", err)
		}
		csproj := `<Project Sdk="Microsoft.NET.Sdk">
			<PropertyGroup>
				<AssemblyName>Named</AssemblyName>
			</PropertyGroup>
		</Project>`
		if err := os.WriteFile(filepath.Join(projectDir, "Bridge", "BridgeHost.csproj"), []byte(csproj), 0644); err != nil {
			t.Fatalf("failed to write csproj: %v", err)
		}

		settings, err := ResolveBridgeSettings(projectDir, "Debug", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if settings.SerializerName != "Named" {
			t.Errorf("SerializerName = %q, want csproj AssemblyName fallback", settings.SerializerName)
		}
		if settings.DotnetConfig != "Debug" {
			t.Errorf("DotnetConfig = %q, want override", settings.DotnetConfig)
		}
		if settings.SdkRoot != sdkRoot {
			t.Errorf("SdkRoot = %q, want %q", settings.SdkRoot, sdkRoot)
		}
		if settings.RootAssemblyPath != filepath.Join(settings.BridgeDir, "bin", "Debug", "net10.0", "Named.dll") {
			t.Errorf("RootAssemblyPath = %q", settings.RootAssemblyPath)
		}
	})
}
