/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MetaplaySdkRootEnvVar is the environment variable that points the bridge build at
// the Metaplay SDK root when the plugin tree is copied out of the SDK (same variable
// the SDK's own metaplay-bridge pre-build step honors).
const MetaplaySdkRootEnvVar = "METAPLAY_SDK_ROOT"

// ValidateSdkRoot checks that a directory looks like a Metaplay SDK root: it must
// contain the .NET tools the bridge build spawns (Dotnet/SerializerGen, Dotnet/MirrorGen)
// and the MetaplayUnreal plugin.
func ValidateSdkRoot(sdkRoot string) error {
	if sdkRoot == "" {
		return fmt.Errorf("SDK root is empty")
	}
	for _, required := range []string{
		filepath.Join("Dotnet", "SerializerGen"),
		filepath.Join("Dotnet", "MirrorGen"),
		filepath.Join("Unreal", "MetaplayUnreal"),
	} {
		if _, err := os.Stat(filepath.Join(sdkRoot, required)); err != nil {
			return fmt.Errorf("the SDK tools were not found under %s (missing %s): is the directory a MetaplaySDK root?", sdkRoot, required)
		}
	}
	return nil
}

// resolveSdkRoot resolves the Metaplay SDK root for a bridge build, in priority order:
//
//  1. the explicit override (the --sdk-root flag),
//  2. the METAPLAY_SDK_ROOT environment variable,
//  3. the bridge project's <Import> of the MetaplayUnreal.BridgeHost.props recipe: the
//     recipe lives at <sdk-root>/Unreal/MetaplayUnreal/BridgeBuild/, so the SDK root is
//     three levels up from it (this works both for projects inside the SDK tree like the
//     TestHost and for external projects referencing the SDK by relative path like the
//     HelloUnreal sample),
//  4. a metaplay-project.yaml found walking up from the Unreal project directory, using
//     its sdkRootDir (relative to the directory containing the file).
func resolveSdkRoot(override string, bridgeCsProjPath string, csproj *CsprojDoc, projectDir string) (string, error) {
	// 1. Explicit flag override.
	if override != "" {
		sdkRoot, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("failed to resolve --sdk-root: %w", err)
		}
		if err := ValidateSdkRoot(sdkRoot); err != nil {
			return "", err
		}
		return sdkRoot, nil
	}

	// 2. Environment variable.
	if envRoot := os.Getenv(MetaplaySdkRootEnvVar); envRoot != "" {
		sdkRoot, err := filepath.Abs(envRoot)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %s: %w", MetaplaySdkRootEnvVar, err)
		}
		if err := ValidateSdkRoot(sdkRoot); err != nil {
			return "", err
		}
		return sdkRoot, nil
	}

	// 3. Derive from the shared recipe import in the bridge csproj.
	if propsPath := csproj.PropsImportPath(); propsPath != "" {
		propsAbs := filepath.Join(filepath.Dir(bridgeCsProjPath), fromMsBuildPath(propsPath))
		if _, err := os.Stat(propsAbs); err == nil {
			// The recipe lives at <sdk-root>/Unreal/MetaplayUnreal/BridgeBuild/MetaplayUnreal.BridgeHost.props.
			sdkRoot := filepath.Clean(filepath.Join(filepath.Dir(propsAbs), "..", "..", ".."))
			if err := ValidateSdkRoot(sdkRoot); err == nil {
				return sdkRoot, nil
			}
		}
	}

	// 4. Walk up from the Unreal project directory looking for metaplay-project.yaml
	//    and use its sdkRootDir.
	if sdkRoot, err := resolveSdkRootFromProjectConfig(projectDir); err == nil {
		return sdkRoot, nil
	}

	return "", fmt.Errorf("could not resolve the Metaplay SDK root for the bridge build")
}

// resolveSdkRootFromProjectConfig walks up from dir looking for metaplay-project.yaml
// and resolves its sdkRootDir against the directory containing the file.
func resolveSdkRootFromProjectConfig(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		configPath := filepath.Join(absDir, "metaplay-project.yaml")
		data, err := os.ReadFile(configPath)
		if err == nil {
			sdkRootDir := parseSdkRootDirFromProjectConfig(data)
			if sdkRootDir != "" {
				// Absolute sdkRootDir values are used as-is; relative ones resolve
				// against the directory containing the project config.
				sdkRoot := sdkRootDir
				if !filepath.IsAbs(sdkRoot) {
					sdkRoot = filepath.Join(absDir, filepath.FromSlash(sdkRootDir))
				}
				validateErr := ValidateSdkRoot(sdkRoot)
				if validateErr == nil {
					return sdkRoot, nil
				}
				return "", fmt.Errorf("sdkRootDir '%s' in %s does not point to a valid MetaplaySDK root: %w", sdkRootDir, configPath, validateErr)
			}
		}
		parent := filepath.Dir(absDir)
		if parent == absDir {
			return "", fmt.Errorf("no metaplay-project.yaml found upwards from %s", dir)
		}
		absDir = parent
	}
}

// parseSdkRootDirFromProjectConfig extracts the sdkRootDir value from a
// metaplay-project.yaml document. The project config is plain YAML with a well-known
// scalar field; a line-oriented scan avoids a full YAML model dependency here.
func parseSdkRootDirFromProjectConfig(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "sdkRootDir:"); ok {
			value := strings.TrimSpace(rest)
			value = strings.Trim(value, `"'`)
			return value
		}
	}
	return ""
}
