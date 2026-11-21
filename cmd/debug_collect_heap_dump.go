/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	utilexec "k8s.io/client-go/util/exec"
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
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argPodName, "POD", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")

	cmd := &cobra.Command{
		Use:     "collect-heap-dump [ENVIRONMENT] [POD] [flags]",
		Aliases: []string{"heap-dump"},
		Short:   "Collect a heap dump from a running server pod",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Collect a heap dump from a running .NET server pod using dotnet-gcdump or dotnet-dump.

			WARNING: This operation is very intrusive as it completely freezes the target process
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
			metaplay debug collect-heap-dump nimbly

			# Collect heap dump from pod 'service-0'.
			metaplay debug collect-heap-dump nimbly service-0

			# Use 'dotnet-dump' for full process dump instead of 'dotnet-gcdump'.
			metaplay debug collect-heap-dump nimbly --mode=dump

			# Specify custom output path. Use .gcdump extension for gcdump mode, and no extension for dump mode.
			metaplay debug collect-heap-dump nimbly -o /path/to/output.gcdump
			metaplay debug collect-heap-dump nimbly --mode=dump -o /path/to/core_250901_093000

			# Don't ask for confirmation on the operation.
			metaplay debug collect-heap-dump nimbly --yes
		`),
	}
	debugCmd.AddCommand(cmd)

	cmd.Flags().StringVarP(&o.flagOutputPath, "output", "o", "", "Output path for the heap dump file (default: dump-YYYYMMDD-hhmmss.gcdump for gcdump mode, core_YYMMDD_HHMMSS for dump mode)")
	cmd.Flags().StringVar(&o.flagCollectMode, "mode", "gcdump", "Collection mode: 'gcdump' (managed heap) or 'dump' (full process dump)")
	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip heap size warning and proceed with dump")
}

func (o *debugCollectHeapDumpOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate collection mode
	if o.flagCollectMode != "gcdump" && o.flagCollectMode != "dump" {
		return clierrors.NewUsageErrorf("Invalid collection mode '%s'", o.flagCollectMode).
			WithSuggestion("Use --mode=gcdump for managed heap or --mode=dump for full process dump")
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
		switch o.flagCollectMode {
		case "gcdump":
			if actualExtension != ".gcdump" {
				return clierrors.NewUsageErrorf("Invalid file extension '%s' for gcdump mode", actualExtension).
					WithSuggestion("Use .gcdump extension for gcdump mode, e.g., 'dump.gcdump'")
			}
		case "dump":
			if actualExtension != "" {
				return clierrors.NewUsageErrorf("Invalid file extension '%s' for dump mode", actualExtension).
					WithSuggestion("Use no extension for dump mode, e.g., 'core_250901_093000'")
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

	// Create and manage debug container in the server pod. It is stopped at the end, or after an
	// hour if the CLI does not get to stop it.
	debugContainerName, cleanup, err := kubeutil.CreateIdleDebugContainer(cmd.Context(), kubeCli, pod.Name, metaplayServerContainerName, time.Hour)
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

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Collect Heap Dump"))
	log.Info().Msg("")
	log.Info().Msgf("Target pod:         %s", styles.RenderTechnical(pod.Name))
	log.Info().Msgf("Collection mode:    %s", styles.RenderTechnical(o.flagCollectMode))
	log.Info().Msgf("Process heap size:  %s", styles.RenderTechnical(fmt.Sprintf("%.2f GB", processInfo.MemoryGB)))
	log.Info().Msgf("Estimated duration: %s", styles.RenderTechnical(formatDuration(int(estimatedDurationSeconds))))
	log.Info().Msgf("Output file:        %s", styles.RenderTechnical(o.flagOutputPath))
	log.Info().Msg("")

	// Warn about process freezing unless --yes is used
	if !o.flagYes {
		log.Warn().Msg(styles.RenderAttention("⚠️ WARNING: This operation will completely freeze the server process!"))
		log.Warn().Msg("The process will be unresponsive for the entire duration of the operation.")
		log.Warn().Msg("")

		// Ask for confirmation
		fmt.Print("Are you sure you want to continue? [y/N] ")
		var response string
		_, _ = fmt.Scanln(&response)
		if !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") {
			log.Info().Msg(styles.RenderError("❌ Operation canceled"))
			return fmt.Errorf("heap dump collection cancelled by user")
		}
		log.Info().Msg("")
	}

	// Create task runner for the collection process
	runner := tui.NewTaskRunner()

	// Collect and retrieve heap dump using task runner
	o.collectAndRetrieveHeapDump(cmd.Context(), kubeCli, pod.Name, debugContainerName, processInfo, runner)

	// Run the tasks
	if err := runner.Run(cmd.Context()); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Heap dump collected successfully!"))
	log.Info().Msgf("  Output file: %s", styles.RenderTechnical(o.flagOutputPath))
	return nil
}

// Helper function to collect and retrieve heap dump using task runner
func (o *debugCollectHeapDumpOpts) collectAndRetrieveHeapDump(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, processInfo *kubeutil.ServerProcessInfo, runner *tui.TaskRunner) {
	// With mode==gcdump, the dump file gets written to /tmp.
	// With mode==dump, the dump file gets written to /proc/<pid>/root/tmp.
	remoteDumpDir := "/tmp"
	if o.flagCollectMode == "dump" {
		// Access the target container filesystem via the server process root
		remoteDumpDir = fmt.Sprintf("/proc/%d/root/tmp", processInfo.Pid)
	}
	remotePath := remoteDumpDir + "/" + filepath.Base(o.flagOutputPath)

	const reenableHealthChecksCmd = "curl localhost:8585/setOverride/healthz?mode=Passthrough"

	// What the cleanup tasks need to know
	overrideSet := false    // The health checks may be forced to "Success"
	collectStarted := false // The collector was started in the pod
	collectEnded := false   // The collector is known to have ended, successfully or not

	// Set healthz probe to success mode
	runner.AddTask("Disable health checks", func(output *tui.TaskOutput) error {
		_, _, err := kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
			"curl localhost:8585/setOverride/healthz?mode=Success", // Undone by reenableHealthChecksCmd
		)
		// A cancelled request may have set the override before it was cancelled
		overrideSet = err == nil || errors.Is(err, context.Canceled)
		if err != nil {
			return fmt.Errorf("failed to set healthz probe mode: %w", err)
		}
		return nil
	})

	// Collect heap dump
	var dumpDuration time.Duration
	runner.AddTask(fmt.Sprintf("Collect %s dump", o.flagCollectMode), func(output *tui.TaskOutput) error {
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

		// Not cancelled with the command: the collector would go on in the pod, keeping the server
		// frozen and writing the dump file until it finishes. Only then can the health checks be
		// re-enabled and the file removed. After an interrupt, wait at most 10 minutes plus 3 times
		// the estimated duration.
		estimated := time.Duration(processInfo.MemoryGB * 10 * float64(time.Second))
		collectStarted = true
		_, _, err := kubeutil.ExecInDebugContainerUntilDone(ctx, 10*time.Minute+3*estimated, kubeCli, podName, debugContainerName, collectCmd)
		dumpDuration = time.Since(startTime)
		if err != nil {
			// An exit code means that the collector's process ended. That ends a gcdump, which
			// dotnet-gcdump does itself. It does not end a full dump: createdump, a child of the
			// server process, goes on writing it after dotnet-dump has exited.
			if _, exited := errors.AsType[utilexec.CodeExitError](err); exited && o.flagCollectMode == "gcdump" {
				collectEnded = true
			}
			return fmt.Errorf("failed to collect heap dump: %w", err)
		}
		collectEnded = true

		// Calculate and log dump rate
		dumpSeconds := dumpDuration.Seconds()
		dumpRate := processInfo.MemoryGB / dumpSeconds
		output.AppendLinef("Collection took %.1f seconds (%.2f GB/s)", dumpSeconds, dumpRate)
		return nil
	})

	// Reset healthz probe
	runner.AddCleanupTask("Re-enable health checks", func(ctx context.Context, output *tui.TaskOutput) error {
		if !overrideSet {
			return nil // Nothing to undo
		}
		if collectStarted && !collectEnded {
			// The collection failed or was given up, possibly while the collector still freezes the
			// server: re-enabling the health checks now could get the server restarted mid-dump.
			return clierrors.Newf("Left the health checks of pod %s disabled, because the heap dump collector may still be running and freezing the server", podName).
				WithDetails(
					"The health checks report healthy until the override expires, 30 minutes after it was set",
					"The collector may still write the dump to "+remotePath,
				).
				WithSuggestion("Once the collector has finished, re-enable the health checks by running in the pod: " + reenableHealthChecksCmd + ", and remove the dump file")
		}
		_, _, err := kubeutil.ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName, reenableHealthChecksCmd)
		if err != nil {
			return clierrors.Wrapf(err, "Failed to re-enable the health checks of pod %s, which report healthy until the override expires 30 minutes after it was set", podName).
				WithSuggestion("To re-enable them sooner, run in the pod: " + reenableHealthChecksCmd)
		}
		return nil
	})

	// Copy heap dump to local machine
	downloaded := false
	runner.AddTask("Download heap dump", func(output *tui.TaskOutput) error {
		err := kubeutil.CopyFileFromDebugPod(ctx, output, kubeCli, podName, debugContainerName, remoteDumpDir, filepath.Base(o.flagOutputPath), o.flagOutputPath, 3)
		if err != nil {
			return fmt.Errorf("failed to copy heap dump: %w", err)
		}
		downloaded = true
		return nil
	})

	// Remove the remote file, also when the download failed
	runner.AddCleanupTask("Remove heap dump from the pod", func(ctx context.Context, output *tui.TaskOutput) error {
		if !collectStarted {
			return nil // No file was written
		}
		return removeFromPod(ctx, kubeCli, podName, debugContainerName, "heap dump", o.flagOutputPath, downloaded, remotePath)
	})
}

// removeFromPod removes a collected file from the pod, explaining a failure: the file uses the
// pod's disk space until it is removed, and the error says whether the local copy is complete.
func removeFromPod(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName, what, localPath string, downloaded bool, remotePaths ...string) error {
	err := kubeutil.RemoveFilesInDebugContainer(ctx, kubeCli, podName, debugContainerName, remotePaths...)
	if err == nil {
		return nil
	}
	message := fmt.Sprintf("Failed to remove the %s from pod %s", what, podName)
	if downloaded {
		message = fmt.Sprintf("The %s was saved to %s, but removing it from pod %s failed", what, localPath, podName)
	}
	return clierrors.Wrap(err, message).
		WithDetails(remotePaths...).
		WithSuggestion("The file uses the pod's disk space until it is removed or the pod restarts. Remove it, or restart the pod")
}

// formatDuration converts a duration in seconds to a human-readable string
func formatDuration(seconds int) string {
	h, m, s := seconds/3600, seconds/60%60, seconds%60

	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
