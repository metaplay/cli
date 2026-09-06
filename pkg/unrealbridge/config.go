/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package unrealbridge implements the .NET-side build integration for Unreal projects
// consuming the MetaplayUnreal plugin: parsing the project's MetaplayBridge.json and
// bridge host .csproj, resolving the Metaplay SDK root, and the shared logic of the
// 'metaplay build serializer' and 'metaplay build unreal-bridge' commands. The commands
// are native Go ports of the SDK's MetaplayUnreal/BridgeBuild/metaplay-bridge.sh (and
// .ps1) pre-build step, so the same pipeline runs on Linux, macOS, and Windows without
// a shell.
package unrealbridge

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BridgeConfigFileName is the bridge declaration file every Unreal project consuming
// the MetaplayUnreal plugin has next to its .uproject.
const BridgeConfigFileName = "MetaplayBridge.json"

// DefaultStageDirRel is the canonical staging directory (relative to the Unreal project
// directory) the MetaplayUnreal plugin loads and stages the bridge artifacts from.
const DefaultStageDirRel = "Binaries/ThirdParty/MetaplayBridge"

// DefaultDotnetConfiguration is the dotnet configuration used when MetaplayBridge.json
// does not declare one.
const DefaultDotnetConfiguration = "Release"

// DefaultTargetFramework matches the target framework pinned by the shared bridge host
// recipe (MetaplayUnreal.BridgeHost.props).
const DefaultTargetFramework = "net10.0"

// DefaultSerializerDirName is the default directory for the serializer artifacts,
// relative to the bridge project directory.
const DefaultSerializerDirName = "Serializer"

// BridgeConfig is the parsed MetaplayBridge.json of an Unreal project.
type BridgeConfig struct {
	BridgeProject  string `json:"BridgeProject"`
	SerializerName string `json:"SerializerName"`
	Configuration  string `json:"Configuration"`
	MirrorHeader   string `json:"MirrorHeader"`
	StageDir       string `json:"StageDir"`
}

// CsprojDoc is the subset of MSBuild project properties the bridge build needs from a
// game's bridge host .csproj. Values follow MSBuild's last-one-wins semantics.
type CsprojDoc struct {
	XMLName        xml.Name              `xml:"Project"`
	PropertyGroups []CsprojPropertyGroup `xml:"PropertyGroup"`
	ImportElements []CsprojImport        `xml:"Import"`
}

// CsprojPropertyGroup is one <PropertyGroup> of a bridge host .csproj.
type CsprojPropertyGroup struct {
	AssemblyName              string `xml:"AssemblyName"`
	TargetFramework           string `xml:"TargetFramework"`
	MetaplaySerializerName    string `xml:"MetaplaySerializerName"`
	MetaplaySerializerDirName string `xml:"MetaplaySerializerDirName"`
}

// CsprojImport is one <Import> element of a bridge host .csproj.
type CsprojImport struct {
	Project string `xml:"Project,attr"`
}

// propsFileName is the shared bridge host recipe a game's bridge project imports; the
// SDK root is derived from its location (three levels up from BridgeBuild/).
const propsFileName = "MetaplayUnreal.BridgeHost.props"

// BridgeSettings is the fully resolved .NET-side build configuration of an Unreal
// project consuming the MetaplayUnreal plugin.
type BridgeSettings struct {
	// The Unreal project directory (where MetaplayBridge.json lives), absolute.
	ProjectDir string
	// Absolute path to MetaplayBridge.json.
	BridgeConfigPath string
	// Absolute path to the game's bridge host .csproj.
	BridgeCsProjPath string
	// Directory of the bridge host project, absolute.
	BridgeDir string
	// Serializer application name: the serializer assembly is Metaplay.Generated.<name>.
	SerializerName string
	// dotnet configuration (Debug/Release).
	DotnetConfig string
	// Declared USTRUCT mirror header path relative to the project dir, or empty.
	MirrorHeaderRel string
	// Staging directory, relative to the project dir.
	StageDirRel string
	// Staging directory, absolute.
	StageDir string
	// Assembly name of the bridge host project.
	AssemblyName string
	// Target framework of the bridge host project (e.g. net10.0).
	TargetFramework string
	// Serializer artifacts directory, absolute.
	SerializerDir string
	// Prebuilt serializer assembly path (Metaplay.Generated.<name>.dll).
	SerializerDllPath string
	// Built bridge host assembly the serializer is generated from.
	RootAssemblyPath string
	// Resolved Metaplay SDK root directory (the MetaplaySDK directory).
	SdkRoot string
}

// SerializerHashPath returns the path of the serializer hash sidecar file.
func (s *BridgeSettings) SerializerHashPath() string {
	return s.SerializerDllPath + ".hash"
}

// SerializerGenProject returns the SDK's SerializerGen tool project path.
func (s *BridgeSettings) SerializerGenProject() string {
	return filepath.Join(s.SdkRoot, "Dotnet", "SerializerGen")
}

// MirrorGenProject returns the SDK's MirrorGen tool project path.
func (s *BridgeSettings) MirrorGenProject() string {
	return filepath.Join(s.SdkRoot, "Dotnet", "MirrorGen")
}

// StagedLibraries lists the NativeAOT bridge libraries staged into the staging
// directory for the given runtime identifier.
func (s *BridgeSettings) StagedLibraries(rid string) ([]string, error) {
	return filepath.Glob(filepath.Join(s.StageDir, NativeLibraryGlob(rid)))
}

// ExpectedHashPath returns the staged expected-serializer hash path: the serializer
// hash implied by the current game code, compared by the plugin at Metaplay_Init
// against the hash the loaded bridge library was published with.
func (s *BridgeSettings) ExpectedHashPath() string {
	return filepath.Join(s.StageDir, "expected.hash")
}

// fromMsBuildPath converts an MSBuild project path (Windows-style backslash
// separators are the MSBuild convention on all hosts) into a native path, so that
// e.g. a csproj authored on Windows resolves the same on Linux.
func fromMsBuildPath(p string) string {
	return filepath.FromSlash(strings.ReplaceAll(p, "\\", "/"))
}

// ParseBridgeConfig parses a MetaplayBridge.json document.
func ParseBridgeConfig(data []byte) (*BridgeConfig, error) {
	var config BridgeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", BridgeConfigFileName, err)
	}
	return &config, nil
}

// ParseCsProj parses the bridge-relevant properties from a bridge host .csproj
// document. Namespaces (legacy MSBuild format) are tolerated by stripping default
// xmlns declarations before parsing.
func ParseCsProj(data []byte) (*CsprojDoc, error) {
	// SDK-style projects have no default xmlns; legacy format does. Strip default
	// namespace declarations so the local-name based struct tags match either way.
	nsRegex := regexp.MustCompile(`xmlns="[^"]*"`)
	stripped := nsRegex.ReplaceAll(data, nil)

	var doc CsprojDoc
	if err := xml.Unmarshal(stripped, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse csproj XML: %w", err)
	}
	return &doc, nil
}

// AssemblyName returns the effective assembly name: the last <AssemblyName> in the
// document, or the project file's base name when not declared.
func (doc *CsprojDoc) AssemblyName(csprojPath string) string {
	name := ""
	for _, group := range doc.PropertyGroups {
		if group.AssemblyName != "" {
			name = group.AssemblyName
		}
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(csprojPath), filepath.Ext(csprojPath))
	}
	return name
}

// LastProperty returns the last non-empty value of a property across the document's
// property groups (MSBuild's last-one-wins semantics).
func (doc *CsprojDoc) LastProperty(get func(CsprojPropertyGroup) string) string {
	value := ""
	for _, group := range doc.PropertyGroups {
		if v := get(group); v != "" {
			value = v
		}
	}
	return value
}

// PropsImportPath returns the <Import Project="..."> path referencing the shared
// MetaplayUnreal.BridgeHost.props recipe, or empty when the project does not import it.
func (doc *CsprojDoc) PropsImportPath() string {
	for _, imp := range doc.ImportElements {
		path := strings.ReplaceAll(imp.Project, "\\", "/")
		if strings.HasSuffix(path, propsFileName) {
			return imp.Project
		}
	}
	return ""
}

// ResolveBridgeSettings resolves the full bridge build configuration for an Unreal
// project directory. dotnetConfigOverride and sdkRootOverride correspond to the
// command-line overrides (empty string = not overridden).
func ResolveBridgeSettings(projectDir string, dotnetConfigOverride string, sdkRootOverride string) (*BridgeSettings, error) {
	// Resolve and validate the Unreal project directory.
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project directory: %w", err)
	}
	info, err := os.Stat(absProjectDir)
	if err != nil {
		return nil, fmt.Errorf("project directory '%s' does not exist: %w", projectDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project directory '%s' is not a directory", projectDir)
	}

	// Read MetaplayBridge.json.
	bridgeConfigPath := filepath.Join(absProjectDir, BridgeConfigFileName)
	configData, err := os.ReadFile(bridgeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("no %s under %s: %w", BridgeConfigFileName, absProjectDir, err)
	}
	config, err := ParseBridgeConfig(configData)
	if err != nil {
		return nil, err
	}
	if config.BridgeProject == "" {
		return nil, fmt.Errorf("%s (%s) does not declare \"BridgeProject\" (the relative path to the game's bridge host .csproj)", BridgeConfigFileName, bridgeConfigPath)
	}

	// Locate the bridge host project.
	bridgeCsProjPath := filepath.Join(absProjectDir, fromMsBuildPath(config.BridgeProject))
	csprojData, err := os.ReadFile(bridgeCsProjPath)
	if err != nil {
		return nil, fmt.Errorf("the bridge project declared in %s was not found: %s", BridgeConfigFileName, bridgeCsProjPath)
	}
	csproj, err := ParseCsProj(csprojData)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", bridgeCsProjPath, err)
	}
	bridgeDir := filepath.Dir(bridgeCsProjPath)

	// Defaults: serializer name and assembly name from the csproj (the shared recipe's
	// defaults), Release configuration, canonical staging directory.
	assemblyName := csproj.AssemblyName(bridgeCsProjPath)
	csprojSerializerName := csproj.LastProperty(func(g CsprojPropertyGroup) string { return g.MetaplaySerializerName })
	serializerName := config.SerializerName
	if serializerName == "" {
		serializerName = csprojSerializerName
	}
	if serializerName == "" {
		serializerName = assemblyName
	}

	dotnetConfig := dotnetConfigOverride
	if dotnetConfig == "" {
		dotnetConfig = config.Configuration
	}
	if dotnetConfig == "" {
		dotnetConfig = DefaultDotnetConfiguration
	}

	stageDirRel := config.StageDir
	if stageDirRel == "" {
		stageDirRel = DefaultStageDirRel
	}
	stageDir := filepath.Join(absProjectDir, filepath.FromSlash(stageDirRel))

	targetFramework := csproj.LastProperty(func(g CsprojPropertyGroup) string { return g.TargetFramework })
	if targetFramework == "" {
		targetFramework = DefaultTargetFramework
	}

	serializerDirName := csproj.LastProperty(func(g CsprojPropertyGroup) string { return g.MetaplaySerializerDirName })
	if serializerDirName == "" {
		serializerDirName = DefaultSerializerDirName
	}
	serializerDir := filepath.Join(bridgeDir, serializerDirName)

	// Resolve the SDK root: flag override > METAPLAY_SDK_ROOT env > the csproj's import
	// of the shared recipe (its SDK root property derives from the file location) >
	// metaplay-project.yaml found upwards from the project dir.
	sdkRoot, err := resolveSdkRoot(sdkRootOverride, bridgeCsProjPath, csproj, absProjectDir)
	if err != nil {
		return nil, err
	}

	return &BridgeSettings{
		ProjectDir:        absProjectDir,
		BridgeConfigPath:  bridgeConfigPath,
		BridgeCsProjPath:  bridgeCsProjPath,
		BridgeDir:         bridgeDir,
		SerializerName:    serializerName,
		DotnetConfig:      dotnetConfig,
		MirrorHeaderRel:   config.MirrorHeader,
		StageDirRel:       stageDirRel,
		StageDir:          stageDir,
		AssemblyName:      assemblyName,
		TargetFramework:   targetFramework,
		SerializerDir:     serializerDir,
		SerializerDllPath: filepath.Join(serializerDir, fmt.Sprintf("Metaplay.Generated.%s.dll", serializerName)),
		RootAssemblyPath:  filepath.Join(bridgeDir, "bin", dotnetConfig, targetFramework, assemblyName+".dll"),
		SdkRoot:           sdkRoot,
	}, nil
}
