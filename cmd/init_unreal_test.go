/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/unrealbridge"
)

func TestInitUnrealGameNameValidation(t *testing.T) {
	valid := []string{"MyGame", "A", "Game2"}
	invalid := []string{"", "mygame", "My Game", "My_Game", "MyGame!", "2Fast", "my-game"}

	for _, name := range valid {
		if !initUnrealGameNameRegex.MatchString(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}
	for _, name := range invalid {
		if initUnrealGameNameRegex.MatchString(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

// renderUnrealProjectFixture renders the full template set into a temp directory and
// returns the directory.
func renderUnrealProjectFixture(t *testing.T) string {
	t.Helper()
	targetDir := t.TempDir()
	data := initUnrealTemplateData{
		GameName:            "MyGame",
		EngineVersion:       "5.8",
		SdkRelFromProject:   "../../MetaplaySDK",
		SdkRelFromBridge:    "../../../MetaplaySDK",
		SdkRelFromGameLogic: "../../../MetaplaySDK",
		HostRid:             "linux-x64",
		HostLibExt:          ".so",
	}
	if err := renderUnrealTemplates(data, targetDir); err != nil {
		t.Fatalf("renderUnrealTemplates returned error: %v", err)
	}
	return targetDir
}

func TestRenderUnrealTemplates_FileSet(t *testing.T) {
	targetDir := renderUnrealProjectFixture(t)

	expectedFiles := []string{
		"MyGame.uproject",
		"MetaplayBridge.json",
		".gitignore",
		"README.md",
		"Config/DefaultEngine.ini",
		"Config/DefaultGame.ini",
		"Source/MyGame.Target.cs",
		"Source/MyGameEditor.Target.cs",
		"Source/MyGame/MyGame.Build.cs",
		"Source/MyGame/Private/MyGameModule.cpp",
		"Source/MyGameEditor/MyGameEditor.Build.cs",
		"Source/MyGameEditor/Private/MyGameEditorModule.cpp",
		"Bridge/MyGame.Bridge.csproj",
		"Bridge/MyGameBridgeModule.cs",
		"Bridge/OfflineEnvironmentConfigProvider.cs",
		"GameLogic/GameLogic.csproj",
		"SharedCode/GlobalOptions.cs",
		"SharedCode/Player/PlayerModel.cs",
		"SharedCode/Player/PlayerActions.cs",
		"SharedCode/Player/PlayerModelListeners.cs",
	}
	for _, file := range expectedFiles {
		if _, err := os.Stat(filepath.Join(targetDir, filepath.FromSlash(file))); err != nil {
			t.Errorf("expected scaffolded file %s: %v", file, err)
		}
	}
}

func TestRenderUnrealTemplates_Uproject(t *testing.T) {
	targetDir := renderUnrealProjectFixture(t)

	data, err := os.ReadFile(filepath.Join(targetDir, "MyGame.uproject"))
	if err != nil {
		t.Fatalf("failed to read uproject: %v", err)
	}
	var uproject struct {
		EngineAssociation string                  `json:"EngineAssociation"`
		Modules           []struct{ Name string } `json:"Modules"`
		Plugins           []struct {
			Name    string
			Enabled bool
		} `json:"Plugins"`
		AdditionalPluginDirectories []string `json:"AdditionalPluginDirectories"`
	}
	if err := json.Unmarshal(data, &uproject); err != nil {
		t.Fatalf("failed to parse uproject JSON: %v\n%s", err, data)
	}
	if uproject.EngineAssociation != "5.8" {
		t.Errorf("EngineAssociation = %q", uproject.EngineAssociation)
	}
	if len(uproject.Modules) != 2 || uproject.Modules[0].Name != "MyGame" || uproject.Modules[1].Name != "MyGameEditor" {
		t.Errorf("Modules = %+v", uproject.Modules)
	}
	if len(uproject.Plugins) != 1 || uproject.Plugins[0].Name != "MetaplayUnreal" || !uproject.Plugins[0].Enabled {
		t.Errorf("Plugins = %+v", uproject.Plugins)
	}
	if len(uproject.AdditionalPluginDirectories) != 1 || uproject.AdditionalPluginDirectories[0] != "../../MetaplaySDK/Unreal" {
		t.Errorf("AdditionalPluginDirectories = %+v", uproject.AdditionalPluginDirectories)
	}
}

func TestRenderUnrealTemplates_BridgeConfigAndCsproj(t *testing.T) {
	targetDir := renderUnrealProjectFixture(t)

	// The MetaplayBridge.json must parse with the unrealbridge settings resolver and
	// point at the scaffolded bridge project.
	configData, err := os.ReadFile(filepath.Join(targetDir, "MetaplayBridge.json"))
	if err != nil {
		t.Fatalf("failed to read MetaplayBridge.json: %v", err)
	}
	config, err := unrealbridge.ParseBridgeConfig(configData)
	if err != nil {
		t.Fatalf("failed to parse MetaplayBridge.json: %v", err)
	}
	if config.BridgeProject != "Bridge/MyGame.Bridge.csproj" {
		t.Errorf("BridgeProject = %q", config.BridgeProject)
	}
	if config.SerializerName != "MyGame" {
		t.Errorf("SerializerName = %q", config.SerializerName)
	}
	if config.MirrorHeader != "Source/MyGame/Public/MetaplayBridgeModels.h" {
		t.Errorf("MirrorHeader = %q", config.MirrorHeader)
	}

	// The scaffolded bridge csproj must parse and reference the SDK relatively.
	csprojData, err := os.ReadFile(filepath.Join(targetDir, "Bridge", "MyGame.Bridge.csproj"))
	if err != nil {
		t.Fatalf("failed to read bridge csproj: %v", err)
	}
	csproj, err := unrealbridge.ParseCsProj(csprojData)
	if err != nil {
		t.Fatalf("failed to parse bridge csproj: %v", err)
	}
	if got := csproj.AssemblyName("MyGame.Bridge.csproj"); got != "MyGame.Bridge" {
		t.Errorf("AssemblyName = %q", got)
	}
	if got := csproj.LastProperty(func(g unrealbridge.CsprojPropertyGroup) string { return g.MetaplaySerializerName }); got != "MyGame" {
		t.Errorf("MetaplaySerializerName = %q", got)
	}
	importPath := csproj.PropsImportPath()
	if importPath == "" {
		t.Fatal("the scaffolded bridge csproj does not import MetaplayUnreal.BridgeHost.props")
	}
	if !strings.Contains(filepath.ToSlash(importPath), "../../../MetaplaySDK/Unreal/MetaplayUnreal/BridgeBuild/MetaplayUnreal.BridgeHost.props") {
		t.Errorf("props import path = %q", importPath)
	}

	// Both .NET projects must attach the SDK's prebuilt Metaplay analyzers (the
	// integration-type source generator) the same way MetaplaySDK's own
	// Directory.Build.targets attaches them for projects inside the SDK tree.
	for _, csprojPath := range []string{
		filepath.Join(targetDir, "Bridge", "MyGame.Bridge.csproj"),
		filepath.Join(targetDir, "GameLogic", "GameLogic.csproj"),
	} {
		data, err := os.ReadFile(csprojPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", csprojPath, err)
		}
		for _, analyzer := range []string{
			"PrebuiltAnalyzers/Metaplay.CodeAnalyzers.dll",
			"PrebuiltAnalyzers/Metaplay.CodeAnalyzers.Shared.dll",
			"PrebuiltAnalyzers/Metaplay.Attributes.dll",
		} {
			if !strings.Contains(string(data), analyzer) {
				t.Errorf("%s does not attach the prebuilt analyzer %s", csprojPath, analyzer)
			}
		}
	}
}

func TestRenderUnrealTemplates_GameSettings(t *testing.T) {
	targetDir := renderUnrealProjectFixture(t)

	ini, err := os.ReadFile(filepath.Join(targetDir, "Config", "DefaultGame.ini"))
	if err != nil {
		t.Fatalf("failed to read DefaultGame.ini: %v", err)
	}
	content := string(ini)
	for _, want := range []string{
		`SerializerAssemblyName="Metaplay.Generated.MyGame"`,
		`NativeAotLibraryPath="Bridge/bin/Release/net10.0/linux-x64/publish/MyGame.Bridge.so"`,
		`EnvironmentId="offline"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("DefaultGame.ini missing %q:\n%s", want, content)
		}
	}
}

func TestRenderUnrealTemplates_NoUnexpandedPlaceholders(t *testing.T) {
	targetDir := renderUnrealProjectFixture(t)

	// No template placeholder may leak into any scaffolded file.
	placeholderRegex := regexp.MustCompile(`\{\{\.`)
	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if placeholderRegex.Match(data) {
			t.Errorf("%s contains an unexpanded template placeholder:\n%s", path, data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}
