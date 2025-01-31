/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// \todo Refactor implementation to use Kubernetes APIs directly, not use 'kubectl'.
// \todo Implement cleaning up ephemeral containers from the target pod.
// \todo Refactor to extract a common framework for the ephemeral containers; use for CPU profiles, too

type CollectHeapDumpOpts struct {
	argEnvironment  string
	argPodName      string
	flagOutputPath  string
	flagCollectMode string
	flagYes         bool
}

func init() {
	o := CollectHeapDumpOpts{}

	cmd := &cobra.Command{
		Use:   "collect-heap-dump ENVIRONMENT [POD] [flags]",
		Short: "[experimental] Collect a heap dump from a running server pod",
		Long: trimIndent(`
			Collect a heap dump from a running .NET server pod using dotnet-gcdump.

			WARNING: This operation is very intrusive as it completely freeze the target process
			for the duration of the operation. This can be from seconds to minutes, depending on
			the process heap size.

			WARNING: This is an experimental feature and interface is likely to change. For now,
			it also requires 'kubectl' to be locally installed to work.

			This command will create a debug container, collect the heap dump using dotnet-gcdump,
			and copy it back to your local machine.

			The health probes will be temporarily modified to always return a success value to
			avoid the kubelet from considering the game server to not be responsive which would
			lead to its termination.

			Arguments:
			- ENVIRONMENT is the target environment in the current project.
			- POD (optional) is name of the pod to target. Must be given when multiple pods exist.
		`),
		Example: trimIndent(`
			# Collect heap dump from the only running pod.
			metaplay debug collect-heap-dump tough-falcons

			# Collect heap dump from pod 'service-0'.
			metaplay debug collect-heap-dump tough-falcons service-0

			# Use 'dotnet-dump' instead of 'dotnet-gcdump'.
			metaplay debug collect-heap-dump tough-falcons --mode dump

			# Specify custom output path on your disk. The .gcdump suffix must be used with dotnet-gcdump!
			metaplay debug collect-heap-dump tough-falcons -o /path/to/output.gcdump

			# Don't ask for confirmation on the operation.
			metaplay debug collect-heap-dump tough-falcons --yes
		`),
		Run: runCommand(&o),
	}
	debugCmd.AddCommand(cmd)

	// FORCE --mode=gcdump as 'dotnet-dump' doesn't produce an output file
	o.flagCollectMode = "gcdump"

	cmd.Flags().StringVarP(&o.flagOutputPath, "output", "o", "", "Output path for the heap dump file (default: dump-YYYYMMDD-hhmmss.gcdump)")
	// cmd.Flags().StringVar(&o.flagCollectMode, "mode", "gcdump", "Collection mode: 'gcdump' or 'dump' (default: gcdump)")
	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip heap size warning and proceed with dump")
}

func (o *CollectHeapDumpOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("exactly one or two arguments must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]
	if len(args) >= 2 {
		o.argPodName = args[1]
	}

	// Validate collection mode
	if o.flagCollectMode != "gcdump" && o.flagCollectMode != "dump" {
		return fmt.Errorf("invalid collection mode '%s': must be either 'gcdump' or 'dump'", o.flagCollectMode)
	}

	// Resolve expected file name extension depending on type.
	extension := "gcdump"
	if o.flagCollectMode == "dump" {
		extension = ""
	}

	// Set default output path if not specified
	if o.flagOutputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		o.flagOutputPath = fmt.Sprintf("dump-%s.%s", timestamp, extension)
	} else {
		// Check the file extension
		actualExtension := filepath.Ext(o.flagOutputPath)
		if actualExtension != "."+extension {
			return fmt.Errorf("invalid extension for output file: expected '.%s' but got '%s'", extension, actualExtension)
		}
	}

	return nil
}

func (o *CollectHeapDumpOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, err := resolveEnvironment(tokenSet, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create environment helper
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get kubeconfig to access the environment
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		log.Error().Msgf("Failed to get kubeconfig: %v", err)
		return err
	}
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Create a clientset to access Kubernetes
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		log.Error().Msgf("Failed to initialize Kubernetes client: %v", err)
		return err
	}

	// Get running game server pods
	kubernetesNamespace := envConfig.getKubernetesNamespace()
	log.Debug().Msgf("Get running game server pods")
	pods, err := clientset.CoreV1().Pods(kubernetesNamespace).List(cmd.Context(), metav1.ListOptions{
		LabelSelector: metaplayGameServerPodLabelSelector,
	})
	if err != nil {
		log.Error().Msgf("Failed to list pods: %v", err)
		return err
	}

	// Fetch all gameserver pods
	gameServerPods := pods.Items
	if len(gameServerPods) == 0 {
		log.Error().Msgf("No game server pods found in namespace %s", kubernetesNamespace)
		return fmt.Errorf("no game server pods found")
	}

	// Resolve target pod name
	targetPodName := o.argPodName
	if targetPodName == "" {
		if len(gameServerPods) == 1 {
			targetPodName = gameServerPods[0].Name
		} else {
			var podNames []string
			for _, pod := range gameServerPods {
				podNames = append(podNames, pod.Name)
			}
			log.Warn().Msgf("Multiple game server pods running: %v", strings.Join(podNames, ", "))
			return fmt.Errorf("specify which pod you want to collect heap dump from with POD argument")
		}
	}

	// Write kubeconfig to a temporary file
	tmpKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("temp-kubeconfig-%d", time.Now().Unix()))
	err = os.WriteFile(tmpKubeconfigPath, []byte(*kubeconfigPayload), 0600)
	if err != nil {
		log.Error().Msgf("Failed to write temporary kubeconfig: %v", err)
		return err
	}
	defer os.Remove(tmpKubeconfigPath)

	// Randomize name for the ephemeral debug container
	debugContainerName, err := createDebugContainerName()
	if err != nil {
		return err
	}

	// Create and manage debug container
	cleanup, err := o.createDebugContainer(tmpKubeconfigPath, kubernetesNamespace, targetPodName, debugContainerName)
	if err != nil {
		return err
	}
	defer cleanup()

	// Get process information
	pid, memoryGB, err := o.getProcessInformation(tmpKubeconfigPath, kubernetesNamespace, targetPodName, debugContainerName)
	if err != nil {
		return err
	}
	estimatedDurationSeconds := memoryGB * 10 // assume 100MB/s (from empirical testing)
	log.Info().Msgf("Game server process heap size is %.2f GB.", memoryGB)
	log.Info().Msgf("Estimated time to complete the operation is: %s", formatDuration(int(estimatedDurationSeconds)))

	// Warn about process freezing unless --yes is used
	if !o.flagYes {
		log.Warn().Msgf("This operation may take a long time and will completely freeze the server process for the duration!")
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
	err = o.collectAndRetrieveHeapDump(tmpKubeconfigPath, kubernetesNamespace, targetPodName, debugContainerName, pid, memoryGB)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully wrote %s", o.flagOutputPath)
	return nil
}

// progressTracker wraps an io.Writer and reports progress
type progressTracker struct {
	w                 io.Writer
	numProcessed      int64
	totalSize         int64
	lastProgress      int
	lastUpdateTime    time.Time
	minUpdateInterval time.Duration
}

func (pt *progressTracker) Write(p []byte) (int, error) {
	n, err := pt.w.Write(p)
	if err != nil {
		return n, err
	}
	pt.numProcessed += int64(n)

	// Calculate current progress percentage
	percentComplete := int(float64(pt.numProcessed) / float64(pt.totalSize) * 100)

	// Ensure we don't exceed 100%
	if percentComplete > 100 {
		percentComplete = 100
	}

	// Update progress if enough time has passed and we have new progress to report.
	// The final 100% is always reported.
	isComplete := pt.numProcessed >= pt.totalSize
	intervalElapsed := time.Since(pt.lastUpdateTime) >= pt.minUpdateInterval
	if isComplete || (intervalElapsed && percentComplete > pt.lastProgress) {
		pt.lastUpdateTime = time.Now()
		pt.lastProgress = percentComplete
		log.Info().Msgf("Copying: %d%%", percentComplete)
	}

	return n, nil
}

// copyFileFromPod copies a file from a pod using a tar pipe, similar to how kubectl cp works internally
func copyFileFromPod(kubeconfigPath, namespace, podName, containerName, srcDir, fileName, destPath string) error {
	// Create the destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	// Command to create compressed tar stream in the container
	tarCmd := fmt.Sprintf("tar czf - -C %s %s", srcDir, fileName)
	args := []string{
		fmt.Sprintf("--kubeconfig=%s", kubeconfigPath),
		fmt.Sprintf("--namespace=%s", namespace),
		"exec",
		podName,
		"-c",
		containerName,
		"--",
		"sh",
		"-c",
		tarCmd,
	}

	// Start kubectl exec process to get tar stream
	log.Debug().Msgf("Execute: kubectl %s", strings.Join(args, " "))
	cmd := exec.Command("kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start kubectl exec: %v", err)
	}

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destFile.Close()

	// Create gzip reader for decompression
	gzReader, err := gzip.NewReader(stdout)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// Extract tar stream
	tr := tar.NewReader(gzReader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cmd.Process.Kill()
			return fmt.Errorf("failed to read tar header: %v", err)
		}

		if hdr.Name != filepath.Base(fileName) {
			continue
		}

		// Create progress tracking writer
		fileSize := hdr.Size
		log.Info().Msgf("Heap dump file size: %s", formatSize(fileSize))

		progressWriter := io.Writer(destFile)
		if fileSize > 0 {
			progressWriter = &progressTracker{
				w:                 destFile,
				totalSize:         fileSize,
				minUpdateInterval: time.Second / 5, // Update at most X times per second
				lastUpdateTime:    time.Now(),
			}
		}

		if _, err := io.Copy(progressWriter, tr); err != nil {
			cmd.Process.Kill()
			return fmt.Errorf("failed to copy file contents: %v", err)
		}
		break
	}

	// Read any error output
	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to read stderr: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("kubectl exec failed: %v, stderr: %s", err, string(stderrBytes))
	}

	return nil
}

// Helper function to create and start a debug container
func (o *CollectHeapDumpOpts) createDebugContainer(kubeconfigPath, namespace, podName, debugContainerName string) (func(), error) {
	log.Info().Msgf("Create debug container %s...", debugContainerName)
	debugArgs := []string{
		fmt.Sprintf("--kubeconfig=%s", kubeconfigPath),
		fmt.Sprintf("--namespace=%s", namespace),
		"debug",
		podName,
		"--profile=general",
		"--image=metaplay/diagnostics:latest",
		"--target=shard-server",
		fmt.Sprintf("--container=%s", debugContainerName),
		"--quiet",
		"--stdin=false",
		"--tty=false",
		"--",
		"sleep",
		"3600", // keep around for max 1 hour (to make sure it gets cleaned up eventually)
	}

	debugCmd := exec.Command("kubectl", debugArgs...)
	if err := debugCmd.Start(); err != nil {
		log.Error().Msgf("Failed to start debug container: %v", err)
		return nil, err
	}

	// Create cleanup function
	cleanup := func() {
		// \todo Figure clean up out later -- we need to remove the ephemeral containers from the pod spec
		// log.Info().Msg("Cleaning up ephemeral debug container...")
		// cleanupArgs := []string{
		// 	fmt.Sprintf("--kubeconfig=%s", kubeconfigPath),
		// 	fmt.Sprintf("--namespace=%s", namespace),
		// 	"debug",
		// 	podName,
		// 	"--quiet",
		// 	"--profile=general",
		// 	"--target=shard-server",
		// 	"--cleanup",
		// }
		// if _, _, err := execCommand("kubectl", cleanupArgs...); err != nil {
		// 	log.Error().Msgf("Failed to cleanup debug container: %v", err)
		// }
	}

	// Wait for the debug container to be ready
	time.Sleep(5 * time.Second)

	return cleanup, nil
}

// Helper function to get process information (PID and memory usage)
func (o *CollectHeapDumpOpts) getProcessInformation(kubeconfigPath, namespace, podName, debugContainerName string) (string, float64, error) {
	// Get PID using kubectl exec in the debug container
	log.Debug().Msgf("Get game server process information...")
	stdout, stderr, err := o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		`dotnet-counters ps | awk 'NR==1 {print $1}'`,
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server process information: %v\nstderr: %s", err, stderr)
		return "", 0, err
	}

	// Parse and validate PID from stdout
	pid := strings.TrimSpace(stdout)
	if pid == "" {
		return "", 0, fmt.Errorf("unable to resolve game server PID: got empty response")
	}

	// Validate that PID is a valid integer
	for _, char := range pid {
		if char < '0' || char > '9' {
			return "", 0, fmt.Errorf("invalid PID format: %s (must be a positive integer)", pid)
		}
	}
	log.Debug().Msgf("Found game server process with PID: %s", pid)

	// Get process memory information using standard Linux tools
	log.Debug().Msgf("Getting process memory information...")
	stdout, stderr, err = o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		fmt.Sprintf("cat /proc/%s/status", pid),
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server memory information: %v\nstderr: %s", err, stderr)
		return "", 0, err
	}

	// log.Debug().Msgf("Full /proc/%s/status:\n%s", pid, stdout)

	// Parse memory information from /proc/[pid]/status
	// Format example:
	// VmSize:   12345678 kB
	// VmRSS:     1234567 kB
	var vmRss int64
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}

		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		if fields[0] == "VmRSS:" {
			vmRss = value
		}
	}

	if vmRss == 0 {
		return "", 0, fmt.Errorf("failed to parse memory information from /proc/status")
	}

	// Convert from kB to GB
	memoryGB := float64(vmRss) / (1024 * 1024.0)
	return pid, memoryGB, nil
}

// Helper function to collect and retrieve heap dump
func (o *CollectHeapDumpOpts) collectAndRetrieveHeapDump(kubeconfigPath, namespace, podName, debugContainerName, pid string, memoryGB float64) error {
	// Set healthz probe to always return success before collecting dump
	log.Info().Msgf("Setting healthz probe to Success mode...")
	_, stderr, err := o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Success",
	)
	if err != nil {
		log.Error().Msgf("Failed to set healthz probe mode: %v\nstderr: %s", err, stderr)
		return err
	}

	// Collect heap dump using kubectl exec in the debug container
	log.Info().Msgf("Collecting heap dump...")
	startTime := time.Now()
	_, stderr, err = o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		fmt.Sprintf("dotnet-%s collect -p %s -o /tmp/%s", o.flagCollectMode, pid, filepath.Base(o.flagOutputPath)),
	)
	dumpDuration := time.Since(startTime)
	if err != nil {
		log.Error().Msgf("Failed to collect heap dump: %v\nstderr: %s", err, stderr)
		return err
	}

	// Reset healthz probe back to passthrough mode
	log.Info().Msgf("Resetting healthz probe to Passthrough mode...")
	_, stderr, err = o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		"curl localhost:8585/setOverride/healthz?mode=Passthrough",
	)
	if err != nil {
		log.Error().Msgf("Failed to reset healthz probe mode: %v\nstderr: %s", err, stderr)
		return err
	}

	// Calculate and print the dump rate
	dumpSeconds := dumpDuration.Seconds()
	dumpRate := memoryGB / dumpSeconds
	log.Info().Msgf("Heap dump took %.1f seconds (%.2f GB/s)", dumpSeconds, dumpRate)

	// Copy the heap dump file from the debug container using tar pipe
	log.Info().Msgf("Retrieving heap dump to local file %s...", o.flagOutputPath)
	err = copyFileFromPod(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		"/tmp",
		filepath.Base(o.flagOutputPath),
		o.flagOutputPath,
	)
	if err != nil {
		log.Error().Msgf("Failed to copy heap dump: %v", err)
		return err
	}

	// Remove the heap dump file from the debug container
	log.Debug().Msgf("Remove heap dump file /tmp/%s from debug container...", filepath.Base(o.flagOutputPath))
	_, stderr, err = o.kubectlExecInDebugContainer(
		kubeconfigPath,
		namespace,
		podName,
		debugContainerName,
		fmt.Sprintf("rm /tmp/%s", filepath.Base(o.flagOutputPath)),
	)
	if err != nil {
		log.Warn().Msgf("Failed to remove heap dump from debug container: %v\nstderr: %s", err, stderr)
		// Don't return error here as the main operation was successful
	}

	return nil
}

// kubectlExecInDebugContainer executes a command in the debug container using kubectl exec
func (o *CollectHeapDumpOpts) kubectlExecInDebugContainer(kubeconfigPath string, namespace string, podName string, debugContainerName string, command string) (string, string, error) {
	args := []string{
		fmt.Sprintf("--kubeconfig=%s", kubeconfigPath),
		fmt.Sprintf("--namespace=%s", namespace),
		"exec",
		podName,
		"-c",
		debugContainerName,
		"--",
		"/bin/bash",
		"--rcfile",
		"/entrypoint.sh",
		"-c",
		command,
	}
	return execCommand("kubectl", args...)
}

func execCommand(name string, args ...string) (string, string, error) {
	log.Debug().Msgf("Execute: %s %s", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("failed to start command: %v", err)
	}

	// Read output
	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		return "", "", fmt.Errorf("failed to read stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		return "", "", fmt.Errorf("failed to read stderr: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return string(stdoutBytes), string(stderrBytes), err
	}

	if cmd.ProcessState.ExitCode() != 0 {
		return string(stdoutBytes), string(stderrBytes), fmt.Errorf("process exited with code %d", cmd.ProcessState.ExitCode())
	}

	return string(stdoutBytes), string(stderrBytes), nil
}

// createDebugContainerName generates a unique debug container name with a random hex string.
func createDebugContainerName() (string, error) {
	// Generate a random 8-byte array
	randomBytes := make([]byte, 8)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Convert the bytes to a hex string
	hexString := hex.EncodeToString(randomBytes)

	// Create the debug container name
	return fmt.Sprintf("debugger-%s", hexString), nil
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

func formatSize(fileSize int64) string {
	const (
		KB = 1 << 10 // 1024 bytes
		MB = 1 << 20 // 1024 KB
		GB = 1 << 30 // 1024 MB
		TB = 1 << 40 // 1024 GB
	)

	switch {
	case fileSize >= TB:
		return fmt.Sprintf("%.2f TB", float64(fileSize)/TB)
	case fileSize >= GB:
		return fmt.Sprintf("%.2f GB", float64(fileSize)/GB)
	case fileSize >= MB:
		return fmt.Sprintf("%.2f MB", float64(fileSize)/MB)
	case fileSize >= KB:
		return fmt.Sprintf("%.2f KB", float64(fileSize)/KB)
	default:
		return fmt.Sprintf("%d B", fileSize)
	}
}
