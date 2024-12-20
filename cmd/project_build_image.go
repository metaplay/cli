package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command line flags
var flagImageTag string
var flagBuildEngine string
var flagArchitecture string
var flagCommitID string
var flagBuildNumber string

// projectBuildImageCmd represents the projectBuild command
var projectBuildImageCmd = &cobra.Command{
	Use:   "build-image",
	Short: "Build docker image of your game server",
	Run:   runBuildImageCmd,
}

func init() {
	projectCmd.AddCommand(projectBuildImageCmd)

	projectBuildImageCmd.Flags().StringVarP(&flagImageTag, "image-tag", "t", "<project>:<timestamp>", "Docker image tag for build, eg, 'mygame:123456'")
	projectBuildImageCmd.Flags().StringVar(&flagBuildEngine, "engine", "", "Docker build engine to use ('buildx' or 'buildkit'), auto-detected if not specified")
	projectBuildImageCmd.Flags().StringVar(&flagArchitecture, "architecture", "amd64", "Architecture of build target, 'amd64' or 'arm64'")
	projectBuildImageCmd.Flags().StringVar(&flagCommitID, "commit-id", "", "Git commit SHA hash or similar, eg, '7d1ebc858b'")
	projectBuildImageCmd.Flags().StringVar(&flagBuildNumber, "build-number", "", "Number identifying this build, eg, '715'")

	projectBuildImageCmd.MarkFlagRequired("image-tag")
}

func runBuildImageCmd(cmd *cobra.Command, extraArgs []string) {
	log.Info().Msg("Building project server docker image..")

	// Find & load the project config file.
	projectDir, projectConfig, err := resolveProjectConfig()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	// Resolve imageTag to use: fill in <timestamp> with current unix time
	// and <project> with the project's slug.
	// \todo use project ID instead of slug when available
	imageTag := strings.Replace(flagImageTag, "<timestamp>", fmt.Sprintf("%d", time.Now().Unix()), -1)
	imageTag = strings.Replace(imageTag, "<project>", projectConfig.ProjectSlug, -1)
	log.Info().Msgf("Building docker image '%s'..", imageTag)

	if strings.HasSuffix(imageTag, ":latest") {
		log.Error().Msg("Building docker image with 'latest' tag is not allowed. Use a commit hash or timestamp instead.")
		os.Exit(1)
	}

	// Log extra arguments.
	if len(extraArgs) > 0 {
		log.Info().Msgf("Extra args to docker: %s", strings.Join(extraArgs, " "))
	}

	// Resolve docker build root directory. All other paths need to be made relative to it.
	buildRootDir := filepath.Join(projectDir, projectConfig.BuildRootDir)

	// Check that sdkRoot is a valid directory
	sdkRootPath := filepath.Join(projectDir, projectConfig.SdkRootDir) //filepath.ToSlash(options.SdkRoot)
	if _, err := os.Stat(sdkRootPath); os.IsNotExist(err) {
		log.Error().Msgf("The Metaplay SDK directory '%s' does not exist.", sdkRootPath)
		os.Exit(2)
	}

	dockerFilePath := filepath.Join(sdkRootPath, "Dockerfile.server")
	if _, err := os.Stat(dockerFilePath); os.IsNotExist(err) {
		log.Error().Msgf("Cannot locate Dockerfile.server at %s.", dockerFilePath)
		os.Exit(2)
	}

	// Check projectRoot directory
	projectBackendDir := filepath.Join(projectDir, projectConfig.BackendDir)
	if _, err := os.Stat(projectBackendDir); os.IsNotExist(err) {
		log.Error().Msgf("Unable to find project backend in '%s'.", projectBackendDir)
		os.Exit(2)
	}

	sharedCodeDir := filepath.Join(projectDir, projectConfig.SharedCodeDir)
	if _, err := os.Stat(sharedCodeDir); os.IsNotExist(err) {
		log.Error().Msgf("The shared code directory (%s) does not exist.", sharedCodeDir)
		os.Exit(2)
	}

	// Resolve target platform
	validArchitectures := []string{"amd64", "arm64"}
	if !contains(validArchitectures, flagArchitecture) {
		log.Error().Msgf("Invalid architecture '%s'. Must be one of %v.", flagArchitecture, validArchitectures)
		os.Exit(2)
	}
	platform := fmt.Sprintf("linux/%s", flagArchitecture)

	// Auto-detect git commit ID
	commitId := flagCommitID
	if commitId == "" {
		commitId = detectEnvVar([]string{
			"GIT_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "CIRCLE_SHA1", "TRAVIS_COMMIT",
			"BUILD_SOURCEVERSION", "BITBUCKET_COMMIT", "BUILD_VCS_NUMBER", "BUILDKITE_COMMIT", "DRONE_COMMIT_SHA",
			"SEMAPHORE_GIT_SHA",
		})
		if commitId != "" {
			log.Info().Msgf("Using auto-detected commit ID: %s", commitId)
		} else {
			log.Warn().Msg("Warning: Failed to auto-detect commit ID. Specify with --commit-id=<id>.")
			commitId = "none" // default if not specified
		}
	}

	// Auto-detect build number
	buildNumber := flagBuildNumber
	if buildNumber == "" {
		buildNumber = detectEnvVar([]string{
			"BUILD_NUMBER", "GITHUB_RUN_NUMBER", "CI_PIPELINE_IID", "CIRCLE_BUILD_NUM", "TRAVIS_BUILD_NUMBER",
			"BUILD_BUILDNUMBER", "BITBUCKET_BUILD_NUMBER", "BUILDKITE_BUILD_NUMBER", "DRONE_BUILD_NUMBER",
			"SEMAPHORE_BUILD_NUMBER",
		})
		if buildNumber != "" {
			log.Info().Msgf("Using auto-detected build number: %s", buildNumber)
		} else {
			log.Warn().Msg("Warning: Failed to auto-detect build number. Specify with --build-number=<number>.")
			buildNumber = "none" // default if not specified
		}
	}

	// Check that docker is installed and running
	log.Info().Msgf("Check if docker is available")
	if err := checkCommand("docker", "info"); err != nil {
		log.Error().Msg("Failed to invoke docker. Ensure docker is installed and running.")
		os.Exit(1)
	}

	// Resolve docker build engine
	log.Debug().Msg("Resolve docker build engine")
	buildEngine, err := resolveBuildEngine(flagBuildEngine)
	if err != nil {
		log.Error().Msgf("Failed to resolve docker build engine: %v", err)
		os.Exit(1)
	}
	log.Info().Msgf("Use docker build engine: %s", buildEngine)

	// Rebase paths to be relative to docker build root.
	rebasedSdkRoot, err := rebasePath(sdkRootPath, buildRootDir)
	if err != nil {
		log.Error().Msgf("Failed to resolve relative path to MetaplaySDK/ from build root: %v", err)
		os.Exit(2)
	}
	rebasedDockerFilePath, err := rebasePath(dockerFilePath, buildRootDir)
	if err != nil {
		log.Error().Msgf("Failed to resolve relative path to Dockerfile.server from build root: %v", err)
		os.Exit(2)
	}
	rebasedProjectRoot, err := rebasePath(projectDir, buildRootDir)
	if err != nil {
		log.Error().Msgf("Failed to resolve relative path to project root from build root: %v", err)
		os.Exit(2)
	}

	// Rebase paths relative to project root dir (where .metaplay.yaml is located).
	rebasedBackendDir, err := rebasePath(projectBackendDir, projectDir)
	if err != nil {
		log.Error().Msgf("Failed to resolve relative path to project backend directory from project root: %v", err)
		os.Exit(2)
	}
	rebasedSharedCodeDir, err := rebasePath(sharedCodeDir, projectDir)
	if err != nil {
		log.Error().Msgf("Failed to resolve relative path to project shared code directory from project root: %v", err)
		os.Exit(2)
	}

	// Resolve final build invocation
	dockerArgs := []string{
		"build", "--pull",
		"-t", imageTag,
		"-f", filepath.ToSlash(rebasedDockerFilePath),
		"--platform", platform,
		"--build-arg", "SDK_ROOT=" + filepath.ToSlash(rebasedSdkRoot),
		"--build-arg", "PROJECT_ROOT=" + filepath.ToSlash(rebasedProjectRoot),
		"--build-arg", "BACKEND_DIR=" + filepath.ToSlash(rebasedBackendDir),
		"--build-arg", "SHARED_CODE_DIR=" + filepath.ToSlash(rebasedSharedCodeDir),
		"--build-arg", fmt.Sprintf("BUILD_NUMBER=%s", buildNumber),
		"--build-arg", fmt.Sprintf("COMMIT_ID=%s", commitId),
	}
	dockerArgs = append(dockerArgs, extraArgs...)
	dockerArgs = append(dockerArgs, ".")
	log.Info().Msgf("Execute: docker %s", strings.Join(dockerArgs, " "))

	// Execute the docker build
	if err := executeCommand(buildRootDir, "docker", dockerArgs...); err != nil {
		log.Error().Msgf("Docker build failed: %v", err)
		os.Exit(1)
	}

	log.Info().Msgf("Successfully built docker image '%s'", imageTag)
}

func contains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}

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
func executeCommand(workingDir string, command string, args ...string) error {
	cmd := exec.Command(command, args...)
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
