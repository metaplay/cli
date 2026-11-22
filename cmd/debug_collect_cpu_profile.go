/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argPodName, "POD", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")
	args.SetExtraArgs(&o.extrArgs, "Passed as-is to 'dotnet-trace'")

	cmd := &cobra.Command{
		Use:     "collect-cpu-profile [ENVIRONMENT] [POD] [flags]",
		Aliases: []string{"cpu-profile"},
		Short:   "Collect a CPU profile from a running server pod",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
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
			metaplay debug collect-cpu-profile nimbly

			# Collect CPU profile from pod 'service-0'.
			metaplay debug collect-cpu-profile nimbly service-0

			# Specify custom output path on your disk.
			metaplay debug collect-cpu-profile nimbly -o /path/to/output.nettrace

			# Specify format (speedscope, chromium, nettrace)
			metaplay debug collect-cpu-profile nimbly --format speedscope

			# Specify duration in seconds (default: 30)
			metaplay debug collect-cpu-profile nimbly --duration 60

			# Pass extra arguments to dotnet-trace (after --)
			metaplay debug collect-cpu-profile nimbly -- --providers Microsoft-Windows-DotNETRuntime:4:4
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

	// Define extension mapping based on format.
	// These match the extensions that dotnet-trace produces for the converted
	// output files (NetTrace/Speedscope/Chromium).
	extensionMapping := map[string]string{
		"NetTrace":   "nettrace",
		"Speedscope": "speedscope.json",
		"Chromium":   "chromium.json",
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

	// Create and manage debug container in the server pod. Everything exec'd into it ends with
	// it, so it must stay up for the whole profile, its conversion and its download. It is stopped
	// at the end, or after its lifetime if the CLI does not get to stop it.
	lifetime := time.Duration(o.flagDuration)*time.Second + time.Hour
	debugContainerName, cleanup, err := kubeutil.CreateIdleDebugContainer(cmd.Context(), kubeCli, pod.Name, metaplayServerContainerName, lifetime)
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
	if err := runner.Run(cmd.Context()); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ CPU profile collected successfully!"))
	log.Info().Msgf("  Output file: %s", styles.RenderTechnical(o.flagOutputPath))
	return nil
}

// Helper function to collect and retrieve CPU profile using task runner
func (o *debugCollectCPUProfileOpts) collectAndRetrieveCPUProfile(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, processInfo *kubeutil.ServerProcessInfo, runner *tui.TaskRunner) error {
	// dotnet-trace always writes the raw nettrace capture to the path given via -o.
	// For Speedscope/Chromium formats, it additionally writes the converted file at
	// a sibling path produced by Path.ChangeExtension(-o, "speedscope.json"|"chromium.json"),
	// which replaces everything after the final dot. To get a predictable converted
	// path, we pass -o with a ".nettrace" extension so the converted file lands at
	// "<base>.speedscope.json" or "<base>.chromium.json".
	remoteDir := "/tmp"
	remoteBaseName := "profile-" + time.Now().Format("20060102-150405")
	remoteNettraceName := remoteBaseName + ".nettrace"
	remoteNettracePath := remoteDir + "/" + remoteNettraceName
	remotePidPath := remoteDir + "/" + remoteBaseName + ".pid"

	var remoteDownloadName string
	switch o.flagFormat {
	case "NetTrace":
		remoteDownloadName = remoteNettraceName
	case "Speedscope":
		remoteDownloadName = remoteBaseName + ".speedscope.json"
	case "Chromium":
		remoteDownloadName = remoteBaseName + ".chromium.json"
	default:
		return fmt.Errorf("unexpected format: %s", o.flagFormat)
	}

	// Collect CPU profile
	runner.AddTask("Collect CPU profile", func(output *tui.TaskOutput) error {
		collectCmd := o.traceCollectCommand(processInfo, remoteNettracePath, remotePidPath)
		log.Debug().Msgf("Execute on remote: %s", collectCmd)

		// Cancelling the exec stream does not stop dotnet-trace in the pod: it would go on for the
		// whole duration and write the profile files after they were removed. So on an interrupt,
		// stop it the way a Ctrl-C would, and wait for it to end.
		stopped := make(chan struct{})
		stopOnInterrupt := context.AfterFunc(ctx, func() {
			defer close(stopped)
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			output.AppendLine("Stopping dotnet-trace")
			if _, _, err := kubeutil.ExecInDebugContainer(stopCtx, kubeCli, podName, debugContainerName, stopTraceCommand(remotePidPath, remoteNettracePath)); err != nil {
				log.Debug().Msgf("Stop dotnet-trace: %v", err)
			}
		})
		_, _, err := kubeutil.ExecInDebugContainerUntilDone(ctx, 2*time.Minute, kubeCli, podName, debugContainerName, collectCmd)
		if !stopOnInterrupt() {
			<-stopped
		}
		if err != nil {
			if ctx.Err() != nil {
				// Stopped by the interrupt, so its exit code is not a failure of its own
				return fmt.Errorf("CPU profile stopped: %w", ctx.Err())
			}
			return fmt.Errorf("failed to collect CPU profile: %w", err)
		}

		return nil
	})

	// Copy CPU profile to local machine
	downloaded := false
	keptInPod := false // The download could not be verified, so the pod's copy is kept
	runner.AddTask("Download CPU profile", func(output *tui.TaskOutput) error {
		err := kubeutil.CopyFileFromDebugPod(ctx, output, kubeCli, podName, debugContainerName, remoteDir, remoteDownloadName, o.flagOutputPath, true, 3)
		if errors.Is(err, kubeutil.ErrCopyUnverified) {
			keptInPod = true
			return unverifiedDownloadError(err, "CPU profile", o.flagOutputPath, podName, remoteDir+"/"+remoteDownloadName)
		}
		if err != nil {
			return fmt.Errorf("failed to copy CPU profile: %w", err)
		}
		downloaded = true
		return nil
	})

	// Remove the nettrace capture and the converted file, if any, also when the download failed
	runner.AddCleanupTask("Remove CPU profile from the pod", func(ctx context.Context, output *tui.TaskOutput) error {
		filesToRemove := []string{remotePidPath}
		if remoteDownloadName != remoteNettraceName {
			filesToRemove = append(filesToRemove, remoteNettracePath)
		}
		if !keptInPod {
			filesToRemove = append(filesToRemove, remoteDir+"/"+remoteDownloadName)
		}
		return removeFromPod(ctx, kubeCli, podName, debugContainerName, "CPU profile", o.flagOutputPath, downloaded, filesToRemove...)
	})

	return nil
}

// formatTraceDuration formats seconds as the hh:mm:ss TimeSpan that dotnet-trace's --duration
// takes, for example 01:00:00 for 3600. Each field must be in its range: dotnet-trace rejects
// 00:60:00.
func formatTraceDuration(seconds int) string {
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// traceCollectCommand returns the shell command that runs dotnet-trace, as the server process's user.
// dotnet-trace replaces the shell that writes its PID to pidPath, so the PID is dotnet-trace's own,
// for stopTraceCommand. Each argument is quoted, so the extra arguments reach dotnet-trace as
// given, also with spaces or shell syntax.
func (o *debugCollectCPUProfileOpts) traceCollectCommand(processInfo *kubeutil.ServerProcessInfo, outputPath, pidPath string) string {
	trace := append([]string{"dotnet-trace", "collect", "-p", strconv.Itoa(processInfo.Pid), "--format", o.flagFormat,
		"--duration", formatTraceDuration(o.flagDuration), "-o", outputPath}, o.extrArgs...)
	command := "echo $$ > " + kubeutil.ShellQuote(pidPath) + "; exec " + kubeutil.ShellJoin(trace)
	if processInfo.Username == "root" {
		return command
	}
	// The $$ reaches the inner sh as it is, the PID of that sh, which exec makes dotnet-trace's.
	return "su " + kubeutil.ShellQuote(processInfo.Username) + " -c " + kubeutil.ShellQuote("sh -c "+kubeutil.ShellQuote(command))
}

// stopTraceCommand returns the shell command that stops the dotnet-trace whose PID is in pidPath and
// that writes to outputPath.
// SIGINT is dotnet-trace's Ctrl-C: it ends the trace session in the server process and finishes
// the file. If dotnet-trace has not ended after 30 seconds, for example while it converts the
// profile, it is killed: after an interrupt, the profile is removed anyway. The PID file can be
// missing for a while when the interrupt comes as the collect command starts. The PID is signalled
// only while its command line still names outputPath: the PID namespace is shared with the game
// server, so after dotnet-trace has exited, the PID can belong to another process.
func stopTraceCommand(pidPath, outputPath string) string {
	return fmt.Sprintf("for i in $(seq 60); do [ -s %[1]s ] && break; sleep 1; done; "+
		"pid=$(cat %[1]s) || exit 0; "+
		"is_trace() { grep -qF -- %[2]s /proc/$pid/cmdline 2>/dev/null; }; "+
		"is_trace && kill -INT $pid; "+
		"for i in $(seq 30); do is_trace || exit 0; sleep 1; done; "+
		"kill -KILL $pid", pidPath, outputPath)
}
