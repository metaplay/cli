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

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type debugCollectCPUProfileOpts struct {
	UsePositionalArgs

	argEnvironment string
	argPodName     string
	extrArgs       []string
	flagOutputPath string
	flagFormat     string
	flagDuration   int
}

func init() {
	o := debugCollectCPUProfileOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argPodName, "POD", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")
	args.SetExtraArgs(&o.extrArgs, "Passed as-is to 'dotnet-trace'")

	cmd := &cobra.Command{
		Use:     "collect-cpu-profile [ENVIRONMENT] [POD] [flags]",
		Aliases: []string{"cpu-profile"},
		Short:   "[preview] Collect a CPU profile from a running server pod",
		Run:     runCommand(&o),
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
		Example: renderExample(`
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
	}
	debugCmd.AddCommand(cmd)

	cmd.Flags().StringVarP(&o.flagOutputPath, "output", "o", "", "Output path for the CPU profile file (default: profile-YYYYMMDD-hhmmss.nettrace)")
	cmd.Flags().StringVar(&o.flagFormat, "format", "nettrace", "Output format: 'nettrace', 'speedscope', or 'chromium'")
	cmd.Flags().IntVar(&o.flagDuration, "duration", 30, "Duration of the trace in seconds")
}

func (o *debugCollectCPUProfileOpts) Prepare(cmd *cobra.Command, args []string) error {
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

func (o *debugCollectCPUProfileOpts) Run(cmd *cobra.Command) error {
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
	debugContainerName, cleanup, err := kubeutil.CreateDebugContainer(cmd.Context(), kubeCli, pod.Name, metaplayServerContainerName, false, false, []string{"sleep", "3600"})
	if err != nil {
		return err
	}
	defer cleanup()

	// Get information about the running server process.
	processInfo, err := kubeutil.GetServerProcessInformation(cmd.Context(), kubeCli, pod.Name, debugContainerName)
	if err != nil {
		return err
	}

	log.Debug().Msgf("Game server process found with PID %d, running as user %s.", processInfo.Pid, processInfo.Username)

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Collect CPU Profile"))
	log.Info().Msg("")
	log.Info().Msgf("Target pod:       %s", styles.RenderTechnical(pod.Name))
	log.Info().Msgf("Profile format:   %s", styles.RenderTechnical(o.flagFormat))
	log.Info().Msgf("Profile duration: %s", styles.RenderTechnical(fmt.Sprintf("%d seconds", o.flagDuration)))
	log.Info().Msgf("Output file:      %s", styles.RenderTechnical(o.flagOutputPath))
	log.Info().Msg("")

	// Create task runner for the collection process
	runner := tui.NewTaskRunner()

	// Collect and retrieve CPU profile using task runner
	err = o.collectAndRetrieveCPUProfile(cmd.Context(), kubeCli, pod.Name, debugContainerName, processInfo, runner)
	if err != nil {
		return err
	}

	// Run the tasks
	if err := runner.Run(); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ CPU profile collected successfully!"))
	log.Info().Msgf("  Output file: %s", styles.RenderTechnical(o.flagOutputPath))
	return nil
}

// Helper function to collect and retrieve CPU profile using task runner
func (o *debugCollectCPUProfileOpts) collectAndRetrieveCPUProfile(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, processInfo *kubeutil.ServerProcessInfo, runner *tui.TaskRunner) error {
	// Collect CPU profile
	var remoteFileName string
	runner.AddTask("Collect CPU profile", func(output *tui.TaskOutput) error {
		// Construct the command to collect the CPU profile.
		// Format the duration as a TimeSpan (00:00:30 for 30 seconds)
		durationFormatted := fmt.Sprintf("00:%02d:%02d", o.flagDuration/60, o.flagDuration%60)
		remoteFileName = filepath.Base(o.flagOutputPath)
		collectCmd := fmt.Sprintf("dotnet-trace collect -p %d --format %s --duration %s -o /tmp/%s",
			processInfo.Pid, o.flagFormat, durationFormatted, remoteFileName)

		// Add extra arguments if provided
		if len(o.extrArgs) > 0 {
			collectCmd += " " + strings.Join(o.extrArgs, " ")
		}
		// If server is running as non-root, collect trace as that user
		if processInfo.Username != "root" {
			collectCmd = fmt.Sprintf("su %s -c 'sh -c \"%s\"'", processInfo.Username, collectCmd)
		}
		log.Debug().Msgf("Execute on remote: %s", collectCmd)

		// Execute the command in the debug container
		_, _, err := kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName, collectCmd)
		if err != nil {
			return fmt.Errorf("failed to collect CPU profile: %v", err)
		}

		return nil
	})

	// Copy CPU profile to local machine & remove remote file (even if copy failed)
	runner.AddTask("Download CPU profile", func(output *tui.TaskOutput) error {
		copyErr := kubeutil.CopyFileFromDebugPod(ctx, output, kubeCli, podName, debugContainerName, "/tmp", remoteFileName, o.flagOutputPath, 3)

		// Remove the remote file, regardless of whether copy was successful
		log.Debug().Msgf("Remove CPU profile file /tmp/%s from debug container...", remoteFileName)
		_, _, removeErr := kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
			fmt.Sprintf("rm /tmp/%s", remoteFileName),
		)
		if removeErr != nil {
			// Don't fail the task for cleanup errors, just log a warning
			log.Warn().Msgf("Failed to remove CPU profile from debug container: %v", removeErr)
		}

		if copyErr != nil {
			return fmt.Errorf("failed to copy CPU profile: %v", copyErr)
		}
		return nil
	})

	return nil
}
