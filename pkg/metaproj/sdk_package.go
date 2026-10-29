/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-version"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/rs/zerolog/log"
)

// A project without 'sdkRootDir' references the Metaplay SDK as NuGet packages, at the version in
// the MetaplaySDKVersion property of its Directory.Build.props. The Metaplay.Server package carries,
// in sdk/, the SDK files that the CLI reads (version.yaml, Dockerfile.server and the files the image
// build copies, the web client presets), laid out as in MetaplaySDK/. The CLI reads them from the
// package itself, without .NET: from the project's local package source, or from the NuGet package
// cache that a restore fills.
const (
	sdkVersionProperty    = "MetaplaySDKVersion"
	sdkPackageID          = "metaplay.server"
	sdkPackageSdkDir      = "sdk"
	localPackageSourceDir = "NuGetPackages"
	nugetCompleteMarker   = ".nupkg.metadata" // NuGet writes it last when it extracts a package into its cache
)

// UsesSdkPackages tells whether the project references the SDK as NuGet packages instead of having
// it in sdkRootDir.
func (project *MetaplayProject) UsesSdkPackages() bool {
	return project.Config.SdkRootDir == ""
}

// RequireSdkSources fails for a project that references the SDK as packages, for a command that
// needs the SDK's sources.
func (project *MetaplayProject) RequireSdkSources(command string) error {
	if !project.UsesSdkPackages() {
		return nil
	}
	return NewSdkSourcesRequiredError(command)
}

// NewSdkSourcesRequiredError is the error for a command that needs the SDK's sources, in a project
// that references the SDK as packages.
func NewSdkSourcesRequiredError(command string) error {
	return clierrors.Newf("'metaplay %s' needs the Metaplay SDK's sources, but this project references the SDK as NuGet packages", command).
		WithDetails("metaplay-project.yaml has no 'sdkRootDir', so the project uses the SDK's packages.").
		WithSuggestion("Use the command in a project that has the SDK's sources in the directory that 'sdkRootDir' points to")
}

// SdkPackageVersion returns the SDK version that the project references the packages at: the
// literal MetaplaySDKVersion in the project's Directory.Build.props.
func (project *MetaplayProject) SdkPackageVersion() (*version.Version, error) {
	propsPath := filepath.Join(project.RelativeDir, "Directory.Build.props")
	value, err := readLiteralMsbuildProperty(propsPath, sdkVersionProperty)
	if err != nil {
		return nil, clierrors.Wrapf(err, "Failed to read the Metaplay SDK version from %s", propsPath).
			WithSuggestion(fmt.Sprintf("Set the SDK version as <%s>X.Y.Z</%s> in a PropertyGroup of Directory.Build.props, or set 'sdkRootDir' in metaplay-project.yaml", sdkVersionProperty, sdkVersionProperty))
	}
	sdkVersion, err := version.NewVersion(value)
	if err != nil {
		return nil, clierrors.Wrapf(err, "Invalid %s '%s' in %s", sdkVersionProperty, value, propsPath)
	}
	return sdkVersion, nil
}

// readLiteralMsbuildProperty returns the value of a property that the file sets exactly once, as a
// literal. The CLI does not evaluate MSBuild, so an expression or a condition is an error.
func readLiteralMsbuildProperty(filePath, name string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	var project struct {
		PropertyGroups []struct {
			Condition  string `xml:"Condition,attr"`
			Properties []struct {
				XMLName   xml.Name
				Condition string `xml:"Condition,attr"`
				Value     string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"PropertyGroup"`
	}
	if err := xml.Unmarshal(content, &project); err != nil {
		return "", err
	}
	var values []string
	for _, group := range project.PropertyGroups {
		for _, property := range group.Properties {
			if property.XMLName.Local != name {
				continue
			}
			if group.Condition != "" || property.Condition != "" {
				return "", fmt.Errorf("%s has a condition, which the metaplay CLI does not evaluate", name)
			}
			values = append(values, strings.TrimSpace(property.Value))
		}
	}
	if len(values) != 1 {
		return "", fmt.Errorf("found %d %s properties, expected exactly one", len(values), name)
	}
	if strings.Contains(values[0], "$(") || strings.Contains(values[0], "@(") || strings.Contains(values[0], "%(") {
		return "", fmt.Errorf("%s is an MSBuild expression ('%s'), which the metaplay CLI does not evaluate", name, values[0])
	}
	return values[0], nil
}

// resolveSdkPackageDir returns the absolute directory of the SDK files in the project's
// Metaplay.Server package. The result, including a failure, is cached.
func (project *MetaplayProject) resolveSdkPackageDir() (string, error) {
	if project.sdkPackageDir == "" && project.sdkPackageDirErr == nil {
		project.sdkPackageDir, project.sdkPackageDirErr = project.findSdkPackageDir()
		if project.sdkPackageDirErr == nil {
			log.Debug().Msgf("Metaplay SDK package directory: %s", project.sdkPackageDir)
		}
	}
	return project.sdkPackageDir, project.sdkPackageDirErr
}

func (project *MetaplayProject) findSdkPackageDir() (string, error) {
	// A project's build references the SDK's projects instead of its packages when MetaplaySDK/ has
	// the SDK's sources (MetaplaySDKReferences in the Blazor template's Directory.Build.props). The
	// CLI and the server image would then use the packages while local builds use the sources.
	sourcesMarker := filepath.Join(project.RelativeDir, "MetaplaySDK", "Backend", "Cloud", "Metaplay.Cloud.csproj")
	if _, err := os.Stat(sourcesMarker); err == nil {
		return "", clierrors.Newf("This project references the SDK as NuGet packages, but MetaplaySDK/ has the SDK's sources, which its build would use instead").
			WithDetails("metaplay-project.yaml has no 'sdkRootDir', so the CLI and the server image build use the SDK's packages.").
			WithSuggestion("Set 'sdkRootDir: MetaplaySDK' in metaplay-project.yaml to use the sources, or remove MetaplaySDK/ to use the packages")
	}

	sdkVersion, err := project.SdkPackageVersion()
	if err != nil {
		return "", err
	}
	// NuGet names a package's files by its id and version in lowercase.
	versionStr := strings.ToLower(sdkVersion.Original())

	// The project's local package source, which the image build restores from too. A repack of a
	// pre-release keeps its version, so this takes precedence over a stale copy in the NuGet cache.
	packagePath := filepath.Join(project.RelativeDir, localPackageSourceDir, fmt.Sprintf("%s.%s.nupkg", sdkPackageID, versionStr))
	if found, err := findFileCaseInsensitive(packagePath); err == nil {
		return extractSdkPackageDir(found, versionStr)
	}

	// The NuGet package cache.
	if packagesRoot, err := nugetPackagesRoot(); err == nil {
		packageDir := filepath.Join(packagesRoot, sdkPackageID, versionStr)
		if _, err := os.Stat(filepath.Join(packageDir, nugetCompleteMarker)); err == nil {
			return filepath.Join(packageDir, sdkPackageSdkDir), nil
		}
	}

	return "", clierrors.Newf("Metaplay.Server %s not found in %s/ or in the NuGet package cache", sdkVersion.Original(), localPackageSourceDir).
		WithSuggestion("Build the project once (for example 'dotnet build'), so that NuGet restores the Metaplay SDK packages")
}

// nugetPackagesRoot returns NuGet's global packages folder: NUGET_PACKAGES, or ~/.nuget/packages.
// \todo Honor 'globalPackagesFolder' in nuget.config.
func nugetPackagesRoot() (string, error) {
	if dir := os.Getenv("NUGET_PACKAGES"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nuget", "packages"), nil
}

// findFileCaseInsensitive returns the path of the file in filePath's directory whose name matches
// filePath's ignoring case, as NuGet package file names keep the id's casing.
func findFileCaseInsensitive(filePath string) (string, error) {
	dir, name := filepath.Split(filePath)
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", fs.ErrNotExist
}

// extractSdkPackageDir extracts the sdk/ directory of a Metaplay.Server package into the CLI's
// cache, unless the cache already has it from the same package file, and returns its path.
func extractSdkPackageDir(packagePath, versionStr string) (string, error) {
	info, err := os.Stat(packagePath)
	if err != nil {
		return "", err
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	targetDir := filepath.Join(cacheRoot, "metaplay", "sdk-packages", sdkPackageID, versionStr)
	sdkDir := filepath.Join(targetDir, sdkPackageSdkDir)
	// A repack of a pre-release keeps its version, so the stamp identifies the package file.
	stamp := fmt.Sprintf("%s %d %d", packagePath, info.Size(), info.ModTime().UnixNano())
	stampPath := filepath.Join(targetDir, ".source")
	if existing, err := os.ReadFile(stampPath); err == nil && string(existing) == stamp {
		return sdkDir, nil
	}

	log.Debug().Msgf("Extract %s/ from %s into %s", sdkPackageSdkDir, packagePath, targetDir)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(targetDir), versionStr+".tmp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	if err := extractZipDir(packagePath, sdkPackageSdkDir, filepath.Join(tempDir, sdkPackageSdkDir)); err != nil {
		return "", clierrors.Wrapf(err, "Failed to extract the SDK files from %s", packagePath)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".source"), []byte(stamp), 0o644); err != nil {
		return "", err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return "", err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		return "", err
	}
	return sdkDir, nil
}

// extractZipDir extracts the entries under prefix/ of a zip archive into targetDir.
func extractZipDir(archivePath, prefix, targetDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	count := 0
	for _, file := range reader.File {
		// NuGet escapes some characters in entry names, but not the ones in the SDK's files.
		name := path.Clean(file.Name)
		relative, ok := strings.CutPrefix(name, prefix+"/")
		if !ok || file.FileInfo().IsDir() {
			continue
		}
		if !filepath.IsLocal(relative) {
			return fmt.Errorf("unsafe entry '%s'", file.Name)
		}
		dst := filepath.Join(targetDir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(file, dst); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return errors.New("the package has no " + prefix + "/ directory")
	}
	return nil
}

func extractZipFile(file *zip.File, dst string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
