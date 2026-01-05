/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Recommended minimum Docker engine version. Old versions have problems with
// cross-architecture builds. 28.0.0 was published in Feb 2025.
var recommendedDockerEngineVersion = semver.MustParse("28.0.0")

// Build docker image for the project.
type buildImageOpts struct {
	UsePositionalArgs

	argImageName      string
	extraArgs         []string
	flagBuildEngine   string
	flagArchitectures []string
	flagCommitID      string
	flagBuildNumber   string
}

func init() {
	o := buildImageOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argImageName, "IMAGE", "Docker image name (optional) and tag, eg, 'mygame:364cff09' or '364cff09'.")
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to docker build.")

	cmd := &cobra.Command{
		Use:     "image [IMAGE] [flags] [-- EXTRA_ARGS]",
		Aliases: []string{"i"},
		Short:   "Build a Docker image of the server components that can be deployed in the cloud",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Build a Docker image of your project to be deployed in the cloud.
			The built image contains both the game server (C# project), the LiveOps
			Dashboard, and the BotClient.

			{Arguments}

			Related commands:
			- 'metaplay deploy server ...' to push and deploy the game server image into a cloud environment.
			- 'metaplay image push ...' to push the built image into a target environment's registry.
		`),
		Example: renderExample(`
			# Build Docker image, produces image named '<projectID>:YYYYMMDD-HHMMSS-COMMIT_ID'.
			# Only recommended when building images manually. In CI, you should always specify the tag explicitly.
			metaplay build image

			# Specify only the tag, produces image named '<projectID>:364cff09'.
			metaplay build image 364cff09

			# Build a project from another directory.
			metaplay -p ../MyProject build image

			# Build docker image with commit ID and build number specified.
			metaplay build image mygame:364cff09 --commit-id=1a27c25753 --build-number=123

			# Build using docker's BuildKit engine (in case buildx isn't available).
			metaplay build image mygame:364cff09 --engine=buildkit

			# Build an image to be run on an arm64 machine.
			metaplay build image mygame:364cff09 --architecture=arm64

			# Build a multi-arch image for both amd64 and arm64 (only supported with 'buildx').
			metaplay build image mygame:364cff09 --architecture=amd64,arm64

			# Pass extra arguments to the docker build.
			metaplay build image mygame:364cff09 -- --build-arg FOO=BAR
		`),
	}

	buildCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagBuildEngine, "engine", "", "Docker build engine to use ('buildx' or 'buildkit'), auto-detected if not specified")
	flags.StringSliceVar(&o.flagArchitectures, "architecture", []string{"amd64"}, "Architectures of build targets (comma-separated), eg, 'amd64' or 'amd64,arm64'")
	flags.StringVar(&o.flagCommitID, "commit-id", "", "Git commit SHA hash or similar, eg, '7d1ebc858b'")
	flags.StringVar(&o.flagBuildNumber, "build-number", "", "Number identifying this build, eg, '715'")
}

func (o *buildImageOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Handle image name.
	if o.argImageName == "" {
		o.argImageName = "<projectID>:<autotag>"
	} else if strings.Contains(o.argImageName, ":") {
		// Full name specified, use as-is
	} else {
		// Only tag specified, prefix with projectID
		o.argImageName = fmt.Sprintf("<projectID>:%s", o.argImageName)
	}

	return nil
}

func (o *buildImageOpts) Run(cmd *cobra.Command) error {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build Docker Image"))
	log.Info().Msg("")

	// Find & load the project config file.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Log extra arguments.
	if len(o.extraArgs) > 0 {
		log.Debug().Msgf("Extra args to docker: %s", strings.Join(o.extraArgs, " "))
	}

	// Auto-detect git commit ID
	commitID := o.flagCommitID
	commitIDBadge := ""
	if commitID == "" {
		commitID = detectEnvVar([]string{
			"GIT_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "CIRCLE_SHA1", "TRAVIS_COMMIT",
			"BUILD_SOURCEVERSION", "BITBUCKET_COMMIT", "BUILD_VCS_NUMBER", "BUILDKITE_COMMIT", "DRONE_COMMIT_SHA",
			"SEMAPHORE_GIT_SHA",
		})
		if commitID != "" {
			commitIDBadge = styles.RenderMuted("(auto-detected)")
		} else {
			commitID = "none" // default if not specified
			commitIDBadge = styles.RenderWarning("[unable to auto-detect; specify with --commit-id=<id>]")
		}
	}

	// Auto-detect build number
	buildNumber := o.flagBuildNumber
	buildNumberBadge := ""
	if buildNumber == "" {
		buildNumber = detectEnvVar([]string{
			"BUILD_NUMBER", "GITHUB_RUN_NUMBER", "CI_PIPELINE_IID", "CIRCLE_BUILD_NUM", "TRAVIS_BUILD_NUMBER",
			"BUILD_BUILDNUMBER", "BITBUCKET_BUILD_NUMBER", "BUILDKITE_BUILD_NUMBER", "DRONE_BUILD_NUMBER",
			"SEMAPHORE_BUILD_NUMBER",
		})
		if buildNumber != "" {
			buildNumberBadge = styles.RenderMuted("(auto-detected)")
		} else {
			buildNumber = "none" // default if not specified
			buildNumberBadge = styles.RenderWarning("[unable to auto-detect; specify with --commit-number=<number>]")
		}
	}

	// Resolve image name to use: fill in <autotag> with YYYYMMDD-HHMMSS[-COMMIT_ID]
	// and <projectID> with the project's human ID.
	log.Debug().Msgf("Image name template: %s", o.argImageName)
	imageName := o.argImageName
	if strings.Contains(imageName, "<autotag>") {
		// Generate auto-tag in format YYYYMMDD-HHMMSS[-COMMIT_ID]
		autoTag := time.Now().UTC().Format("20060102-150405")
		if commitID != "" && commitID != "none" {
			autoTag = fmt.Sprintf("%s-%s", autoTag, commitID)
		}
		imageName = strings.ReplaceAll(imageName, "<autotag>", autoTag)
	}
	imageName = strings.ReplaceAll(imageName, "<projectID>", project.Config.ProjectHumanID)

	if strings.HasSuffix(imageName, ":latest") {
		return clierrors.New("Cannot build image with tag 'latest'").
			WithSuggestion("Use a unique tag like 'mygame:20250131-133012'")
	}

	// Check that docker is installed and running
	log.Debug().Msgf("Check that docker is available")
	err = checkDockerAvailable()
	if err != nil {
		return err
	}

	// Check Docker version: warn if using old versions
	dockerVersionInfo, dockerUpgradeRecommended, err := checkDockerVersion()
	if err != nil {
		log.Warn().Msgf("Warning: Failed to check Docker version: %v", err)
	}

	// Resolve docker build engine
	log.Debug().Msg("Resolve docker build engine")
	buildEngine, err := resolveBuildEngine(o.flagBuildEngine)
	if err != nil {
		return clierrors.Wrap(err, "Invalid Docker build engine").
			WithSuggestion("Use --engine=buildx or --engine=buildkit")
	}

	// Check that the build engine is available.
	err = checkBuildEngineAvailable(buildEngine)
	if err != nil {
		return err
	}

	// Validate target architectures.
	validArchitectures := []string{"amd64", "arm64"}
	if len(o.flagArchitectures) == 0 {
		return clierrors.NewUsageError("No target architecture specified").
			WithSuggestion("Use --architecture=amd64 or --architecture=arm64")
	}
	for _, arch := range o.flagArchitectures {
		if !sliceContains(validArchitectures, arch) {
			return clierrors.NewUsageErrorf("Invalid architecture '%s'", arch).
				WithDetails(fmt.Sprintf("Valid architectures: %v", validArchitectures)).
				WithSuggestion("Use --architecture=amd64 or --architecture=arm64")
		}
	}

	// Only buildx supports building multiple architectures at once.
	if buildEngine == "buildkit" && len(o.flagArchitectures) > 1 {
		return clierrors.NewUsageError("BuildKit does not support multi-architecture builds").
			WithSuggestion("Use --engine=buildx for multi-arch builds, or build for only one architecture")
	}

	// Resolve target platforms.
	platforms := []string{}
	for _, arch := range o.flagArchitectures {
		platforms = append(platforms, fmt.Sprintf("linux/%s", arch))
	}

	// Resolve Docker version badge and show update recommendation
	dockerVersionBadge := ""
	if dockerVersionInfo == nil {
		dockerVersionBadge = styles.RenderWarning("[unable to check version]")
	} else if dockerUpgradeRecommended {
		dockerVersionBadge = styles.RenderWarning("[version is old; upgrade recommended]")
	}

	// Print build info.
	log.Info().Msgf("Project ID:          %s", styles.RenderTechnical(project.Config.ProjectHumanID))
	log.Info().Msgf("Metaplay SDK:        %s", styles.RenderTechnical(project.VersionMetadata.SdkVersion.String()))
	log.Info().Msgf("Docker image:        %s", styles.RenderTechnical(imageName))
	log.Info().Msgf("Commit ID            %s %s", styles.RenderTechnical(commitID), commitIDBadge)
	log.Info().Msgf("Build number:        %s %s", styles.RenderTechnical(buildNumber), buildNumberBadge)
	log.Info().Msgf("Target platform(s):  %s", styles.RenderTechnical(strings.Join(platforms, ", ")))
	log.Info().Msgf("Docker version:      %s %s", styles.RenderTechnical(dockerVersionInfo.Server.Version), dockerVersionBadge)
	log.Info().Msgf("Docker build engine: %s", styles.RenderTechnical(buildEngine))

	// Build the Docker image using the extracted function
	buildParams := buildDockerImageParams{
		project:     project,
		imageName:   imageName,
		buildEngine: buildEngine,
		platforms:   platforms,
		commitID:    commitID,
		buildNumber: buildNumber,
		extraArgs:   o.extraArgs,
	}

	if err := buildDockerImage(buildParams); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ %s %s", styles.RenderSuccess("Successfully built docker image"), styles.RenderTechnical(imageName))
	log.Info().Msg("")
	log.Info().Msg("You can deploy the image to a cloud environment using:")
	log.Info().Msgf(styles.RenderTechnical("  metaplay deploy server ENVIRONMENT %s"), imageName)

	envsIDs := []string{}
	for _, env := range project.Config.Environments {
		envsIDs = append(envsIDs, styles.RenderTechnical(env.HumanID))
	}
	log.Info().Msgf("Available environments: %s", strings.Join(envsIDs, ", "))

	return nil
}

// Check if a value exists in a slice.
func sliceContains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}

// Find the first non-empty environment variable from a list of keys.
// If none of the keys have a value, return an empty string.
func detectEnvVar(keys []string) string {
	for _, key := range keys {
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
	}
	return ""
}

func resolveBuildEngine(engine string) (string, error) {
	validBuildEngines := []string{"buildx", "buildkit"}

	// If not specified, auto-detect
	if engine == "" {
		// Bitbucket doesn't support buildx, fall back to buildkit
		if _, exists := os.LookupEnv("BITBUCKET_PIPELINE_UUID"); exists {
			return "buildkit", nil
		}
		return "buildx", nil
	}

	// Check validity if specified
	for _, validEngine := range validBuildEngines {
		if engine == validEngine {
			return engine, nil
		}
	}

	return "", fmt.Errorf("invalid Docker build engine '%s', must be one of: %v", engine, validBuildEngines)
}

func checkCommand(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %v", err)
	}
	return nil
}

// executeCommand runs a command with the given arguments in the specified working directory.
func executeCommand(workingDir string, env []string, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = workingDir // Set the working directory
	return cmd.Run()
}

// rebasePath calculates a new path for `targetPath` such that it is relative
// to `newBaseDir` instead of current working directory.
func rebasePath(targetPath, newBaseDir string) (string, error) {
	// Resolve absolute directories of new base path & target path.
	absNewBaseDir, err := filepath.Abs(newBaseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute base path: %w", err)
	}
	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute target path: %w", err)
	}

	// Compute the relative path to the new base.
	relativePath, err := filepath.Rel(absNewBaseDir, absTargetPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve relative path: %w", err)
	}

	// log.Debug().Msgf("Rebase %s onto %s -> %s", targetPath, newBaseDir, relativePath)
	// log.Debug().Msgf("  absNewBaseDir=%s, absTargetPath=%s", absNewBaseDir, absTargetPath)

	return relativePath, nil
}

// Check if docker is available and running. Uses a short timeout as 'docker' invocation
// can sometimes hang indefinitely.
func checkDockerAvailable() error {
	// Run 'docker info' in background so we can handle timeouts (docker is known to hang
	// indefinitely in some cases).
	done := make(chan error)
	go func() {
		done <- checkCommand("docker", "info")
	}()

	// Wait for docker to respond .. print a waiting message after 1sec
	select {
	case err := <-done:
		if err != nil {
			return clierrors.Wrap(err, "Docker is not responding").
				WithSuggestion("Make sure Docker Desktop is running, or start the docker daemon")
		}
		return nil
	case <-time.After(time.Second):
		log.Info().Msgf("Waiting for docker daemon to respond...")
	}

	// Wait for 9sec more (for total of 10sec) before timing out
	select {
	case err := <-done:
		if err != nil {
			return clierrors.Wrap(err, "Docker is not responding").
				WithSuggestion("Make sure Docker Desktop is running, or start the docker daemon")
		}
	case <-time.After(9 * time.Second):
		return clierrors.New("Docker daemon timed out").
			WithSuggestion("Docker may be starting up or unresponsive. Try restarting Docker Desktop.")
	}

	return nil
}

// Check that the specified docker build engine is available.
func checkBuildEngineAvailable(buildEngine string) error {
	log.Debug().Msgf("Check that build engine %s is available", buildEngine)

	switch buildEngine {
	case "buildx":
		err := checkCommand("docker", "buildx", "version")
		if err != nil {
			return clierrors.Wrap(err, "Docker buildx is not available").
				WithSuggestion("Install Docker buildx or use --engine=buildkit instead")
		}
	}

	return nil
}

// dockerVersionInfo represents the JSON output from docker version command
type dockerVersionInfo struct {
	Client struct {
		Version    string `json:"Version"`
		ApiVersion string `json:"ApiVersion"`
		GitCommit  string `json:"GitCommit"`
		GoVersion  string `json:"GoVersion"`
		Os         string `json:"Os"`
		Arch       string `json:"Arch"`
		BuildTime  string `json:"BuildTime"`
	} `json:"Client"`
	Server struct {
		Platform struct {
			Name string `json:"Name"`
		} `json:"Platform"`
		Version       string `json:"Version"`
		ApiVersion    string `json:"ApiVersion"`
		MinAPIVersion string `json:"MinAPIVersion"`
		GitCommit     string `json:"GitCommit"`
		GoVersion     string `json:"GoVersion"`
		Os            string `json:"Os"`
		Arch          string `json:"Arch"`
		KernelVersion string `json:"KernelVersion"`
		BuildTime     string `json:"BuildTime"`
	} `json:"Server"`
}

// Check Docker version and return parsed server version
func checkDockerVersion() (*dockerVersionInfo, bool, error) {
	// Get Docker version in JSON format to access server version
	cmd := exec.Command("docker", "version", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get Docker version: %w", err)
	}

	var versionInfo dockerVersionInfo
	if err := json.Unmarshal(output, &versionInfo); err != nil {
		log.Debug().Err(err).Msgf("Could not parse Docker version JSON output")
		return nil, false, nil // Don't fail the build if we can't parse the version
	}

	log.Debug().Msgf("Docker server version: %s", versionInfo.Server.Version)
	engineVersion, err := semver.NewVersion(versionInfo.Server.Version)
	if err != nil {
		log.Debug().Err(err).Msgf("Could not parse server version string: %s", versionInfo.Server.Version)
		return nil, false, nil
	}

	// Recommend upgrade for old versions
	upgradeRecommended := engineVersion.LessThan(recommendedDockerEngineVersion)

	return &versionInfo, upgradeRecommended, nil
}

// buildDockerImageParams contains all parameters needed for building a Docker image
type buildDockerImageParams struct {
	project     *metaproj.MetaplayProject // Metaplay project to build
	imageName   string                    // Name of the built image
	buildEngine string                    // Docker build engine to use (buildx, buildkit)
	platforms   []string                  // Platforms to build for (e.g. linux/amd64, linux/arm64)
	commitID    string                    // Commit ID to use for the build
	buildNumber string                    // Build number to use for the build
	extraArgs   []string                  // Extra arguments to pass to docker build
	target      string                    // Optional: Dockerfile stage to build
}

// buildDockerImage builds a Docker image with the given parameters
func buildDockerImage(params buildDockerImageParams) error {
	// Resolve docker build root directory. All other paths need to be made relative to it.
	buildRootDir := params.project.GetBuildRootDir()

	// Check that sdkRoot is a valid directory
	sdkRootPath := params.project.GetSdkRootDir()
	if _, err := os.Stat(sdkRootPath); os.IsNotExist(err) {
		return clierrors.Newf("Metaplay SDK directory not found: %s", sdkRootPath).
			WithSuggestion("Check that 'sdkRootDir' in metaplay-project.yaml points to the correct location")
	}

	dockerFilePath := filepath.Join(sdkRootPath, "Dockerfile.server")
	if _, err := os.Stat(dockerFilePath); os.IsNotExist(err) {
		return clierrors.Newf("Cannot find Dockerfile.server at %s", dockerFilePath).
			WithSuggestion("Make sure the Metaplay SDK is properly installed")
	}

	// Check project root directory.
	projectBackendDir := params.project.GetBackendDir()
	if _, err := os.Stat(projectBackendDir); os.IsNotExist(err) {
		return clierrors.Newf("Project backend directory not found: %s", projectBackendDir).
			WithSuggestion("Check that 'backendDir' in metaplay-project.yaml points to the correct location")
	}

	// Check SharedCode directory.
	sharedCodeDir := params.project.GetSharedCodeDir()
	if _, err := os.Stat(sharedCodeDir); os.IsNotExist(err) {
		return clierrors.Newf("Shared code directory not found: %s", sharedCodeDir).
			WithSuggestion("Check that 'sharedCodeDir' in metaplay-project.yaml points to the correct location")
	}

	// Rebase paths to be relative to docker build root.
	rebasedSdkRoot, err := rebasePath(sdkRootPath, buildRootDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve path to MetaplaySDK/ from build root")
	}
	rebasedDockerFilePath, err := rebasePath(dockerFilePath, buildRootDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve path to Dockerfile.server from build root")
	}
	rebasedProjectRoot, err := rebasePath(params.project.RelativeDir, buildRootDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve path to project root from build root")
	}

	// Rebase paths relative to project root dir (where metaplay-project.yaml is located).
	rebasedBackendDir, err := rebasePath(projectBackendDir, params.project.RelativeDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve path to backend directory from project root")
	}
	rebasedSharedCodeDir, err := rebasePath(sharedCodeDir, params.project.RelativeDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve path to shared code directory from project root")
	}

	// Silence docker's recomendation messages at end-of-build.
	var dockerEnv []string = os.Environ()
	dockerEnv = append(dockerEnv, "DOCKER_CLI_HINTS=false")

	// Handle build engine differences.
	var buildEngineArgs []string
	if params.buildEngine == "buildkit" {
		dockerEnv = append(dockerEnv, "DOCKER_BUILDKIT=1")
		buildEngineArgs = []string{"build"}
	} else if params.buildEngine == "buildx" {
		buildEngineArgs = []string{"buildx", "build", "--load"}
	} else {
		log.Panic().Msgf("Unsupported docker build engine: %s", params.buildEngine)
	}

	// Resolve .NET runtime version to build project for, expects '<major>.<minor>'.
	projectDotnetVersionSegments := params.project.Config.DotnetRuntimeVersion.Segments()
	projectDotnetVersion := fmt.Sprintf("%d.%d", projectDotnetVersionSegments[0], projectDotnetVersionSegments[1])

	// Resolve final docker build invocation
	dockerArgs := append(
		buildEngineArgs,
		[]string{
			"--pull",
			"-t", params.imageName,
			"-f", filepath.ToSlash(rebasedDockerFilePath),
			"--build-arg", "SDK_ROOT=" + filepath.ToSlash(rebasedSdkRoot),
			"--build-arg", "PROJECT_ROOT=" + filepath.ToSlash(rebasedProjectRoot),
			"--build-arg", "BACKEND_DIR=" + filepath.ToSlash(rebasedBackendDir),
			"--build-arg", "SHARED_CODE_DIR=" + filepath.ToSlash(rebasedSharedCodeDir),
			"--build-arg", "METAPLAY_DOTNET_SDK_VERSION=" + projectDotnetVersion,
			"--build-arg", fmt.Sprintf("PROJECT_ID=%s", params.project.Config.ProjectHumanID),
			"--build-arg", fmt.Sprintf("BUILD_NUMBER=%s", params.buildNumber),
			"--build-arg", fmt.Sprintf("COMMIT_ID=%s", params.commitID),
		}...,
	)

	// If target platform is specified, set it explicitly.
	if len(params.platforms) > 0 {
		dockerArgs = append(
			dockerArgs,
			"--platform", strings.Join(params.platforms, ","))
	}

	// Add target if specified (for multi-stage builds)
	if params.target != "" {
		dockerArgs = append(dockerArgs, "--target", params.target)
	}
	dockerArgs = append(dockerArgs, params.extraArgs...)
	dockerArgs = append(dockerArgs, ".")
	log.Info().Msg("")
	log.Info().Msgf(styles.RenderMuted("docker %s"), strings.Join(dockerArgs, " "))
	log.Info().Msg("")

	// Execute the docker build
	if err := executeCommand(buildRootDir, dockerEnv, "docker", dockerArgs...); err != nil {
		return clierrors.Wrap(err, "Docker build failed").
			WithSuggestion("Check the build output above for details")
	}

	return nil
}
