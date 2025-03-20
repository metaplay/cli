/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type CollectCpuProfileOpts struct {
	UsePositionalArgs

	argEnvironment string
	argPodName     string
	extrArgs       []string
	flagOutputPath string
	flagFormat     string
	flagDuration   int
}

func init() {
	o := CollectCpuProfileOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argPodName, "POD", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")
	args.SetExtraArgs(&o.extrArgs, "Passed as-is to 'dotnet-trace'")

	cmd := &cobra.Command{
		Use:   "collect-cpu-profile [ENVIRONMENT] [POD] [flags]",
		Short: "[preview] Collect a CPU profile from a running server pod",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Collect a CPU profile from a running .NET server pod using dotnet-trace.

			This command will create a debug container, collect the CPU profile using dotnet-trace,
			and copy it back to your local machine.

			The health probes will be temporarily modified to always return a success value to
			avoid the kubelet from considering the game server to not be responsive which would
			lead to its termination.

			{Arguments}
		`),
		Example: trimIndent(`
			# Collect CPU profile from the only running pod.
			metaplay debug collect-cpu-profile tough-falcons

			# Collect CPU profile from pod 'service-0'.
			metaplay debug collect-cpu-profile tough-falcons service-0

			# Specify custom output path on your disk.
			metaplay debug collect-cpu-profile tough-falcons -o /path/to/output.nettrace

			# Specify format (speedscope, chromium, nettrace)
			metaplay debug collect-cpu-profile tough-falcons --format speedscope

			# Specify duration in seconds (default: 30)
			metaplay debug collect-cpu-profile tough-falcons --duration 60

			# Pass extra arguments to dotnet-trace (after --)
			metaplay debug collect-cpu-profile tough-falcons -- --providers Microsoft-Windows-DotNETRuntime:4:4
		`),
		Run: runCommand(&o),
	}
	debugCmd.AddCommand(cmd)

	cmd.Flags().StringVarP(&o.flagOutputPath, "output", "o", "", "Output path for the CPU profile file (default: profile-YYYYMMDD-hhmmss.nettrace)")
	cmd.Flags().StringVar(&o.flagFormat, "format", "nettrace", "Output format: 'nettrace', 'speedscope', or 'chromium'")
	cmd.Flags().IntVar(&o.flagDuration, "duration", 30, "Duration of the trace in seconds")
}

func (o *CollectCpuProfileOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate format
	validFormats := map[string]bool{
		"nettrace":   true,
		"speedscope": true,
		"chromium":   true,
	}

	if !validFormats[o.flagFormat] {
		return fmt.Errorf("invalid format '%s': must be one of 'nettrace', 'speedscope', or 'chromium'", o.flagFormat)
	}

	// Note: dotnet-trace may have different format names than what we expose
	// This mapping ensures we pass the correct format to dotnet-trace
	formatMapping := map[string]string{
		"nettrace":   "NetTrace",
		"speedscope": "Speedscope",
		"chromium":   "Chromium",
	}

	// Update the format to use the correct casing
	o.flagFormat = formatMapping[o.flagFormat]

	// Validate duration
	if o.flagDuration <= 0 {
		return fmt.Errorf("duration must be greater than 0 seconds")
	}
	if o.flagDuration > 3600 {
		return fmt.Errorf("duration must not exceed 3600 seconds (1 hour)")
	}

	log.Debug().Msgf("Using duration: %d seconds (will be formatted as TimeSpan)", o.flagDuration)

	// Define extension mapping based on format
	extensionMapping := map[string]string{
		"NetTrace":   "nettrace",
		"Speedscope": "speedscope.json",
		"Chromium":   "trace.json",
	}

	// Set default output path if not specified
	if o.flagOutputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		extension := extensionMapping[o.flagFormat]
		o.flagOutputPath = fmt.Sprintf("profile-%s.%s", timestamp, extension)
	} else {
		// Check the file extension matches the format
		expectedExt := "." + extensionMapping[o.flagFormat]

		// Simple check for expected extension
		if !strings.HasSuffix(o.flagOutputPath, expectedExt) {
			log.Warn().Msgf("Output filename '%s' doesn't have the expected extension '%s' for format '%s'",
				o.flagOutputPath, expectedExt, o.flagFormat)
		}
	}

	return nil
}

func (o *CollectCpuProfileOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment config.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Resolve target environment & game server.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)
	gameServer, err := targetEnv.GetGameServer(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve target pod (or ask for it if not defined).
	kubeCli, pod, err := resolveTargetPod(gameServer, o.argPodName)
	if err != nil {
		return err
	}

	// Create and manage debug container in the server pod.
	// Keep the container alive for an hour to avoid leaks.
	debugContainerName, cleanup, err := createDebugContainer(cmd.Context(), kubeCli, pod.Name, metaplayServerContainerName, false, false, []string{"sleep", "3600"})
	if err != nil {
		return err
	}
	defer cleanup()

	// Get information about the running server process.
	processInfo, err := getServerProcessInformation(cmd.Context(), kubeCli, pod.Name, debugContainerName)
	if err != nil {
		return err
	}

	log.Info().Msgf("Game server process found with PID %d, running as user %s.", processInfo.pid, processInfo.username)
	log.Info().Msgf("CPU profiling will run for %d seconds (%02d:%02d:%02d).",
		o.flagDuration,
		o.flagDuration/3600,
		(o.flagDuration%3600)/60,
		o.flagDuration%60)

	// Collect and retrieve CPU profile
	err = o.collectAndRetrieveCpuProfile(cmd.Context(), kubeCli, pod.Name, debugContainerName, processInfo)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully wrote %s", o.flagOutputPath)
	return nil
}

// Helper function to collect and retrieve CPU profile - Uses Kubernetes API for exec
func (o *CollectCpuProfileOpts) collectAndRetrieveCpuProfile(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, processInfo *serverProcessInfo) error {
	// Set healthz probe to always return success before collecting profile
	log.Info().Msgf("Setting healthz probe to Success mode...")
	_, _, err := execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Success",
	)
	if err != nil {
		log.Error().Msgf("Failed to set healthz probe mode: %v", err)
		return err
	}

	// Collect CPU profile using dotnet-trace in the debug container
	log.Info().Msgf("Collecting CPU profile...")
	startTime := time.Now()

	// Construct the command to collect the CPU profile
	remoteFileName := filepath.Base(o.flagOutputPath)

	// Build the dotnet-trace command
	// Convert duration to a proper TimeSpan format (e.g., "00:00:30" for 30 seconds)
	durationStr := fmt.Sprintf("%02d:%02d:%02d", o.flagDuration/3600, (o.flagDuration%3600)/60, o.flagDuration%60)
	collectCmd := fmt.Sprintf("dotnet-trace collect --process-id %d --output /tmp/%s --format %s --duration %s",
		processInfo.pid, remoteFileName, o.flagFormat, durationStr)

	// Add any extra arguments
	if len(o.extrArgs) > 0 {
		extraArgsStr := strings.Join(o.extrArgs, " ")
		collectCmd = fmt.Sprintf("%s %s", collectCmd, extraArgsStr)
	}

	// Run as the appropriate user if not root
	if processInfo.username != "root" {
		collectCmd = fmt.Sprintf("su %s -c 'bash -c \"%s\"'", processInfo.username, collectCmd)
	}

	log.Info().Msgf("Executing dotnet-trace with format=%s, duration=%s", o.flagFormat, durationStr)
	log.Debug().Msgf("Full command: %s", collectCmd)

	stdout, stderr, err := execInDebugContainer(ctx, kubeCli, podName, debugContainerName, collectCmd)
	profileDuration := time.Since(startTime)

	// Log the output regardless of error
	if stdout != "" {
		log.Debug().Msgf("Command stdout: %s", stdout)
	}

	if err != nil {
		log.Error().Msgf("Failed to collect CPU profile: %v", err)
		if stderr != "" {
			log.Error().Msgf("Error output: %s", stderr)
		}
		return fmt.Errorf("dotnet-trace failed: %w", err)
	}

	// Check if the output file was created
	_, _, err = execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("test -f /tmp/%s && echo 'File exists'", remoteFileName))
	if err != nil {
		log.Error().Msgf("Output file was not created: %v", err)
		return fmt.Errorf("profile output file was not created")
	}

	// Reset healthz probe back to passthrough mode
	log.Info().Msgf("Resetting healthz probe to Passthrough mode...")
	_, _, err = execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Passthrough",
	)
	if err != nil {
		log.Error().Msgf("Failed to reset healthz probe mode: %v", err)
		return err
	}

	// Calculate and print the profile duration
	log.Info().Msgf("CPU profile collection took %.1f seconds", profileDuration.Seconds())

	// Copy the CPU profile file from the debug container
	log.Info().Msgf("Retrieving CPU profile to local file %s...", o.flagOutputPath)
	err = copyFileFromPod(ctx, kubeCli, podName, debugContainerName, "/tmp", remoteFileName, o.flagOutputPath)
	if err != nil {
		log.Error().Msgf("Failed to copy CPU profile: %v", err)
		return err
	}

	// Remove the CPU profile file from the debug container
	log.Debug().Msgf("Remove CPU profile file /tmp/%s from debug container...", remoteFileName)
	_, _, err = execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("rm /tmp/%s", remoteFileName),
	)
	if err != nil {
		log.Warn().Msgf("Failed to remove CPU profile from debug container: %v", err)
		// Don't return error here as the main operation was successful
	}

	return nil
}
