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
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// \todo Implement cleaning up ephemeral containers from the target pod.
// \todo Refactor to extract a common framework for the ephemeral containers; use for CPU profiles, too

type debugCollectHeapDumpOpts struct {
	UsePositionalArgs

	argEnvironment  string
	argPodName      string
	flagOutputPath  string
	flagCollectMode string
	flagYes         bool
}

func init() {
	o := debugCollectHeapDumpOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argPodName, "POD", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")

	cmd := &cobra.Command{
		Use:   "collect-heap-dump [ENVIRONMENT] [POD] [flags]",
		Short: "[preview] Collect a heap dump from a running server pod",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Collect a heap dump from a running .NET server pod using dotnet-gcdump or dotnet-dump.

			WARNING: This operation is very intrusive as it completely freeze the target process
			for the duration of the operation. This can be from seconds to minutes, depending on
			the process heap size.

			This command will create a debug container, collect the heap dump using the specified
			mode (dotnet-gcdump for managed heap only, or dotnet-dump for full process dump),
			and copy it back to your local machine.

			The health probes will be temporarily modified to always return a success value to
			avoid the kubelet from considering the game server to not be responsive which would
			lead to its termination.

			{Arguments}
		`),
		Example: renderExample(`
			# Collect heap dump from the only running pod.
			metaplay debug collect-heap-dump tough-falcons

			# Collect heap dump from pod 'service-0'.
			metaplay debug collect-heap-dump tough-falcons service-0

			# Use 'dotnet-dump' for full process dump instead of 'dotnet-gcdump'.
			metaplay debug collect-heap-dump tough-falcons --mode dump

			# Specify custom output path. Use .gcdump extension for gcdump mode, and no extension for dump mode.
			metaplay debug collect-heap-dump tough-falcons -o /path/to/output.gcdump
			metaplay debug collect-heap-dump tough-falcons --mode dump -o /path/to/core_250901_093000

			# Don't ask for confirmation on the operation.
			metaplay debug collect-heap-dump tough-falcons --yes
		`),
		Run: runCommand(&o),
	}
	debugCmd.AddCommand(cmd)

	cmd.Flags().StringVarP(&o.flagOutputPath, "output", "o", "", "Output path for the heap dump file (default: dump-YYYYMMDD-hhmmss.gcdump for gcdump mode, core_YYMMDD_HHMMSS for dump mode)")
	cmd.Flags().StringVar(&o.flagCollectMode, "mode", "gcdump", "Collection mode: 'gcdump' (managed heap) or 'dump' (full process dump) (default: gcdump)")
	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip heap size warning and proceed with dump")
}

func (o *debugCollectHeapDumpOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate collection mode
	if o.flagCollectMode != "gcdump" && o.flagCollectMode != "dump" {
		return fmt.Errorf("invalid collection mode '%s': must be either 'gcdump' or 'dump'", o.flagCollectMode)
	}

	// Set default output path if not specified
	if o.flagOutputPath == "" {
		if o.flagCollectMode == "gcdump" {
			// YYYYMMDD-hhmmss for gcdump
			timestamp := time.Now().Format("20060102-150405")
			o.flagOutputPath = fmt.Sprintf("dump-%s.gcdump", timestamp)
		} else {
			// core_YYMMDD_HHMMSS for dump mode (Linux)
			timestamp := time.Now().Format("060102_150405")
			o.flagOutputPath = fmt.Sprintf("core_%s", timestamp)
		}
	} else {
		// Validate file extension based on mode
		actualExtension := filepath.Ext(o.flagOutputPath)
		if o.flagCollectMode == "gcdump" {
			if actualExtension != ".gcdump" {
				return fmt.Errorf("invalid extension for gcdump mode: expected '.gcdump' but got '%s'", actualExtension)
			}
		} else if o.flagCollectMode == "dump" {
			if actualExtension != "" {
				return fmt.Errorf("dump mode must not have a file extension, but got '%s'", actualExtension)
			}
		}
	}

	return nil
}

func (o *debugCollectHeapDumpOpts) Run(cmd *cobra.Command) error {
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

	estimatedDurationSeconds := processInfo.MemoryGB * 10 // assume 100MB/s (from empirical testing)
	log.Info().Msgf("Game server process heap size is %.2f GB.", processInfo.MemoryGB)
	log.Info().Msgf("Estimated time to complete the operation is: %s", formatDuration(int(estimatedDurationSeconds)))

	// Warn about process freezing unless --yes is used
	if !o.flagYes {
		log.Warn().Msgf("This operation will completely freeze the server process for the duration!")
		log.Warn().Msg("Use --yes to skip this check.")

		// Ask for confirmation
		fmt.Print("Are you sure you want to continue? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") {
			return fmt.Errorf("heap dump collection cancelled by user")
		}
	}

	// Collect and retrieve heap dump
	err = o.collectAndRetrieveHeapDump(cmd.Context(), kubeCli, pod.Name, debugContainerName, processInfo)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully wrote %s", o.flagOutputPath)
	return nil
}

// Helper function to collect and retrieve heap dump - Uses Kubernetes API for exec
func (o *debugCollectHeapDumpOpts) collectAndRetrieveHeapDump(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, processInfo *kubeutil.ServerProcessInfo) error {
	// Set healthz probe to always return success before collecting dump
	log.Info().Msgf("Setting healthz probe to Success mode...")
	_, _, err := kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Success",
	)
	if err != nil {
		log.Error().Msgf("Failed to set healthz probe mode: %v", err) // Removed stderr
		return err
	}

	// Collect heap dump using dotnet tools in the debug container
	log.Info().Msgf("Collecting heap dump...")
	startTime := time.Now()

	// Construct the command to collect the heap dump.
	var collectCmd string
	if o.flagCollectMode == "gcdump" {
		collectCmd = fmt.Sprintf("dotnet-gcdump collect -p %d -o /tmp/%s", processInfo.Pid, filepath.Base(o.flagOutputPath))
	} else {
		collectCmd = fmt.Sprintf("dotnet-dump collect -p %d -o /tmp/%s", processInfo.Pid, filepath.Base(o.flagOutputPath))
	}
	if processInfo.Username != "root" {
		collectCmd = fmt.Sprintf("su %s -c 'bash -c \"%s\"'", processInfo.Username, collectCmd)
	}
	log.Debug().Msgf("Execute on remote: %s", collectCmd)

	_, _, err = kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName, collectCmd)
	dumpDuration := time.Since(startTime)
	if err != nil {
		log.Error().Msgf("Failed to collect heap dump: %v", err) // Removed stderr
		return err
	}

	// Reset healthz probe back to passthrough mode
	log.Info().Msgf("Resetting healthz probe to Passthrough mode...")
	_, _, err = kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Passthrough",
	)
	if err != nil {
		log.Error().Msgf("Failed to reset healthz probe mode: %v", err) // Removed stderr
		return err
	}

	// Calculate and print the dump rate
	dumpSeconds := dumpDuration.Seconds()
	dumpRate := processInfo.MemoryGB / dumpSeconds
	log.Info().Msgf("Heap dump took %.1f seconds (%.2f GB/s)", dumpSeconds, dumpRate)

	// With mode==gcdump, the dump file gets written to /tmp.
	// With mode==dump, the dump file gets written to /proc/<pid>/root/tmp.
	remoteDumpDir := "/tmp"
	if o.flagCollectMode == "dump" {
		// Access the target container filesystem via the server process root
		remoteDumpDir = fmt.Sprintf("/proc/%d/root/tmp", processInfo.Pid)
	}

	// Copy the heap dump file from the debug container
	log.Info().Msgf("Retrieving heap dump to local file %s...", o.flagOutputPath)
	err = kubeutil.CopyFileFromDebugPod(ctx, kubeCli, podName, debugContainerName, remoteDumpDir, filepath.Base(o.flagOutputPath), o.flagOutputPath, 3)
	if err != nil {
		log.Error().Msgf("Failed to copy heap dump: %v", err)
		return err
	}

	// Remove the heap dump file from the debug container
	log.Debug().Msgf("Remove heap dump file %s/%s from debug container...", remoteDumpDir, filepath.Base(o.flagOutputPath))
	_, _, err = kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("rm %s/%s", remoteDumpDir, filepath.Base(o.flagOutputPath)),
	)
	if err != nil {
		log.Warn().Msgf("Failed to remove heap dump from debug container: %v", err) // Removed stderr
		// Don't return error here as the main operation was successful
	}

	return nil
}

// formatDuration converts a duration in seconds to a human-readable string
func formatDuration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	} else if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	} else {
		return fmt.Sprintf("%ds", s)
	}
}
