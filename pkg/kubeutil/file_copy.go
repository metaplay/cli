/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// debugRandomFailures enables random failure injection for testing resume logic.
// Set via METAPLAYCLI_DEBUG_COPY_RANDOM_FAIL=1 environment variable.
var debugRandomFailures = os.Getenv("METAPLAYCLI_DEBUG_COPY_RANDOM_FAIL") == "1"

func init() {
	if debugRandomFailures {
		log.Warn().Msg("DEBUG INJECT: Random copy failures ENABLED via METAPLAYCLI_DEBUG_COPY_RANDOM_FAIL=1")
	}
}

// ioProgressTracker wraps an io.Writer and reports progress
type ioProgressTracker struct {
	outWriter         io.Writer
	progressOutput    *tui.TaskOutput
	numProcessed      int64
	totalSize         int64
	lastProgress      int
	lastUpdateTime    time.Time
	minUpdateInterval time.Duration
}

func (pt *ioProgressTracker) Write(p []byte) (int, error) {
	n, err := pt.outWriter.Write(p)
	if err != nil {
		return n, err
	}
	pt.numProcessed += int64(n)

	// Calculate current progress percentage
	percentComplete := min(
		// Ensure we don't exceed 100%
		int(float64(pt.numProcessed)/float64(pt.totalSize)*100), 100)

	// Update progress if enough time has passed and we have new progress to report.
	// The final 100% is always reported.
	isComplete := pt.numProcessed >= pt.totalSize
	intervalElapsed := time.Since(pt.lastUpdateTime) >= pt.minUpdateInterval
	if isComplete || (intervalElapsed && percentComplete > pt.lastProgress) {
		pt.lastUpdateTime = time.Now()
		pt.lastProgress = percentComplete
		pt.progressOutput.AppendLinef("Copy progress: %d%% (%s / %s)", percentComplete, humanizeFileSize(pt.numProcessed), humanizeFileSize(pt.totalSize))
	}

	return n, nil
}

// debugFailingReader wraps a reader and simulates random failures for testing resume logic.
// Used via METAPLAYCLI_DEBUG_COPY_RANDOM_FAIL=1 env var.
type debugFailingReader struct {
	reader    io.Reader
	bytesRead int64
	failAfter int64 // randomly chosen failure point
	hasFailed bool
}

// newDebugFailingReader creates a reader that fails at a random point.
// The failure point is chosen between 10% and 90% of remainingBytes.
func newDebugFailingReader(reader io.Reader, remainingBytes int64) *debugFailingReader {
	// Pick a random point between 10% and 90% of remaining bytes
	minFail := remainingBytes / 10
	maxFail := remainingBytes * 9 / 10
	if maxFail <= minFail {
		maxFail = minFail + 1
	}
	failAfter := minFail + rand.Int63n(maxFail-minFail)

	log.Debug().Msgf("DEBUG INJECT: will SIMULATE FAILURE after %s (%d bytes)", humanizeFileSize(failAfter), failAfter)
	return &debugFailingReader{
		reader:    reader,
		failAfter: failAfter,
	}
}

func (r *debugFailingReader) Read(p []byte) (int, error) {
	if !r.hasFailed && r.bytesRead >= r.failAfter {
		r.hasFailed = true
		log.Warn().Msgf("DEBUG INJECT: SIMULATED FAILURE after %s transferred", humanizeFileSize(r.bytesRead))
		return 0, fmt.Errorf("simulated random failure after %d bytes (debug mode)", r.bytesRead)
	}
	n, err := r.reader.Read(p)
	r.bytesRead += int64(n)
	return n, err
}

// getRemoteFileSize retrieves the size of a file on the pod using stat.
func getRemoteFileSize(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, filePath string) (int64, error) {
	command := fmt.Sprintf("stat -c '%%s' %s", filePath)
	stdout, stderr, err := ExecInDebugContainer(ctx, kubeCli, podName, containerName, command)
	if err != nil {
		return 0, fmt.Errorf("failed to get file size: %w (stderr: %s)", err, stderr)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse file size '%s': %w", stdout, err)
	}
	return size, nil
}

// getRemoteFileMD5 calculates the MD5 checksum of a file on the pod.
func getRemoteFileMD5(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, filePath string) (string, error) {
	command := fmt.Sprintf("md5sum %s | cut -d' ' -f1", filePath)
	stdout, stderr, err := ExecInDebugContainer(ctx, kubeCli, podName, containerName, command)
	if err != nil {
		return "", fmt.Errorf("failed to get file MD5: %w (stderr: %s)", err, stderr)
	}
	return strings.TrimSpace(stdout), nil
}

// calculateLocalFileMD5 calculates the MD5 checksum of a local file.
func calculateLocalFileMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// streamFileFromOffset streams file content from a byte offset using dd.
// This enables resumable transfers by skipping already-transferred bytes.
func streamFileFromOffset(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, srcPath string, offset int64, useCompression bool) (io.Reader, func() error, error) {
	// Use dd with skip_bytes to start from the offset, then optionally compress
	var command string
	if useCompression {
		command = fmt.Sprintf("dd if=%s bs=1M skip=%d iflag=skip_bytes 2>/dev/null | gzip -c", srcPath, offset)
	} else {
		command = fmt.Sprintf("dd if=%s bs=1M skip=%d iflag=skip_bytes 2>/dev/null", srcPath, offset)
	}

	reader, outStream := io.Pipe()

	req := kubeCli.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   []string{"sh", "-c", command},
			Container: containerName,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeCli.RestConfig, "POST", req.URL())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create executor: %w", err)
	}

	go func() {
		streamStart := time.Now()
		log.Debug().Msgf("SPDY stream started (offset=%d): pod=%s container=%s path=%s", offset, podName, containerName, srcPath)

		var stderrBuf bytes.Buffer
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: outStream,
			Stderr: &stderrBuf,
			Tty:    false,
		})

		elapsed := time.Since(streamStart)
		if err != nil {
			log.Debug().Msgf("SPDY stream failed after %v: %v", elapsed, err)
			if stderrBuf.Len() > 0 {
				outStream.CloseWithError(fmt.Errorf("stream failed: %w (stderr: %s)", err, strings.TrimSpace(stderrBuf.String())))
			} else {
				outStream.CloseWithError(fmt.Errorf("stream failed: %w", err))
			}
		} else {
			log.Debug().Msgf("SPDY stream completed normally after %v", elapsed)
			outStream.Close()
		}
	}()

	// Setup reader with optional decompression
	if useCompression {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		return gzReader, func() error { return gzReader.Close() }, nil
	}
	return reader, func() error { return nil }, nil
}

// streamFileFromPod streams a tar file from a pod and returns an io.Reader for the file contents
// If useCompression is true, will use gzip compression (requires shell in the container)
// Always follows symlinks (uses -h)
func streamFileFromPod(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName string, useCompression bool) (io.Reader, func() error, int64, error) {
	// Construct the tar command to stream the file from the pod (with or without compression).
	tarFlags := map[bool]string{true: "chfz", false: "chf"}[useCompression]
	// command := []string{"tar", tarFlags, "-", "-C", srcDir, fileName}
	command := []string{"sh", "-c", fmt.Sprintf("tar %s - -C %s %s", tarFlags, srcDir, fileName)}

	reader, outStream := io.Pipe()

	// Construct the exec request
	req := kubeCli.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: containerName,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeCli.RestConfig, "POST", req.URL())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create executor: %w", err)
	}

	go func() {
		streamStart := time.Now()
		log.Debug().Msgf("SPDY stream started: pod=%s container=%s file=%s/%s", podName, containerName, srcDir, fileName)

		// Capture stderr from the remote command to diagnose tar failures
		var stderrBuf bytes.Buffer
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: outStream,
			Stderr: &stderrBuf,
			Tty:    false,
		})

		elapsed := time.Since(streamStart)
		if err != nil {
			log.Debug().Msgf("SPDY stream failed after %v: %v (type: %T)", elapsed, err, err)
			if stderrBuf.Len() > 0 {
				stderrStr := strings.TrimSpace(stderrBuf.String())
				log.Debug().Msgf("Remote command stderr: %s", stderrStr)
				outStream.CloseWithError(fmt.Errorf("stream failed after %v: %w (stderr: %s)", elapsed, err, stderrStr))
			} else {
				outStream.CloseWithError(fmt.Errorf("stream failed after %v: %w", elapsed, err))
			}
		} else {
			// Log stderr even on success (warnings from tar)
			if stderrBuf.Len() > 0 {
				log.Debug().Msgf("Remote command stderr (non-fatal): %s", strings.TrimSpace(stderrBuf.String()))
			}
			log.Debug().Msgf("SPDY stream completed normally after %v", elapsed)
			outStream.Close()
		}
	}()

	// Setup the appropriate reader based on compression
	var tarReader *tar.Reader
	var closer func() error

	if useCompression {
		// Create gzip reader for decompression
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		tarReader = tar.NewReader(gzReader)
		closer = func() error { return gzReader.Close() }
	} else {
		// Direct tar reader without compression
		tarReader = tar.NewReader(reader)
		closer = func() error { return nil } // No closer needed for raw reader
	}

	// Extract tar stream
	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			closer()
			return nil, nil, 0, fmt.Errorf("failed to read tar header: %v", err)
		}
		if hdr.Name != filepath.Base(fileName) {
			continue
		}
		// Return the reader for this file, plus a closer
		return tarReader, closer, hdr.Size, nil
	}
	closer()
	return nil, nil, 0, fmt.Errorf("file %s not found in tar stream", fileName)
}

// resumeState tracks transfer progress for in-memory resume support.
type resumeState struct {
	fileSize         int64
	bytesTransferred int64
}

// attemptResumableFileCopy performs a single file copy attempt with resume support.
// If state is provided and has bytesTransferred > 0, it will resume from that offset.
// Returns the number of new bytes transferred in this attempt.
func attemptResumableFileCopy(ctx context.Context, output *tui.TaskOutput, kubeCli *envapi.KubeClient, podName, containerName, srcPath, destPath string, state *resumeState) (int64, error) {
	offset := state.bytesTransferred
	fileSize := state.fileSize

	// Open file: append mode if resuming, create if starting fresh
	var destFile *os.File
	var err error
	if offset > 0 {
		destFile, err = os.OpenFile(destPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// If we can't open for append, start fresh
			log.Debug().Msgf("Failed to open file for resume, starting fresh: %v", err)
			offset = 0
			state.bytesTransferred = 0
			destFile, err = os.Create(destPath)
		}
	} else {
		destFile, err = os.Create(destPath)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destFile.Close()

	// Stream from offset using dd
	reader, closer, err := streamFileFromOffset(ctx, kubeCli, podName, containerName, srcPath, offset, true)
	if err != nil {
		return 0, err
	}
	defer closer()

	remainingBytes := fileSize - offset

	// Wrap with debug failure injection if enabled
	if debugRandomFailures && remainingBytes > 0 {
		reader = newDebugFailingReader(reader, remainingBytes)
	}
	if offset > 0 {
		output.SetHeaderLines([]string{
			fmt.Sprintf("File size: %s (resuming from %s)", humanizeFileSize(fileSize), humanizeFileSize(offset)),
		})
		output.AppendLinef("Resuming transfer from %s...", humanizeFileSize(offset))
	} else {
		output.SetHeaderLines([]string{
			fmt.Sprintf("File size: %s", humanizeFileSize(fileSize)),
		})
	}

	// Determine update interval: faster in interactive mode, slower in non-interactive mode
	interval := map[bool]time.Duration{
		true:  time.Second / 5,
		false: 5 * time.Second,
	}[tui.IsInteractiveMode()]

	// Create progress tracker for the file copying.
	progressTracker := &ioProgressTracker{
		outWriter:         destFile,
		progressOutput:    output,
		numProcessed:      offset, // Start from resumed position
		totalSize:         fileSize,
		minUpdateInterval: interval,
		lastUpdateTime:    time.Now(),
	}

	// Copy the file & track progress.
	copyStart := time.Now()
	bytesThisAttempt, copyErr := io.Copy(progressTracker, reader)

	// Update state with new progress
	state.bytesTransferred = offset + bytesThisAttempt

	if copyErr != nil {
		elapsed := time.Since(copyStart)
		remaining := fileSize - state.bytesTransferred
		throughputMBps := float64(bytesThisAttempt) / elapsed.Seconds() / (1 << 20)

		output.AppendLinef("Transfer interrupted: %v (transferred %s of %s total)",
			copyErr, humanizeFileSize(state.bytesTransferred), humanizeFileSize(fileSize))

		// Log diagnostic details
		log.Debug().Msgf("Transfer failed after %v: copied %d/%d bytes this attempt (%.1f MB/s), total progress: %d/%d",
			elapsed, bytesThisAttempt, remainingBytes, throughputMBps, state.bytesTransferred, fileSize)
		log.Debug().Msgf("Bytes remaining: %d (%.2f MB)", remaining, float64(remaining)/(1<<20))

		// Detect gzip stream truncation
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			log.Debug().Msg("Gzip stream truncated - connection likely dropped before transfer completed")
		}

		// Check if container was terminated
		checkContainerTermination(ctx, kubeCli, podName, containerName)

		return bytesThisAttempt, fmt.Errorf("transfer interrupted: %w", copyErr)
	}

	// Log successful transfer statistics
	elapsed := time.Since(copyStart)
	throughputMBps := float64(bytesThisAttempt) / elapsed.Seconds() / (1 << 20)
	log.Debug().Msgf("Transfer completed: %d bytes in %v (%.1f MB/s)", bytesThisAttempt, elapsed, throughputMBps)

	return bytesThisAttempt, nil
}

// CopyFileFromDebugPod copies a file from a pod with resumable transfer support.
// Only works with the ephemeral debug container as this requires shell on the target pod.
// Use numAttempts > 1 to retry the copy operation in case of failure - retries will resume
// from where the previous attempt left off rather than starting from scratch.
// File integrity is verified via MD5 checksum after transfer completes.
func CopyFileFromDebugPod(ctx context.Context, output *tui.TaskOutput, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName, destPath string, numAttempts int) error {
	if numAttempts < 1 {
		return fmt.Errorf("numAttempts must be at least 1")
	}

	// Create the destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	// Use forward slash for remote Linux path (not filepath.Join which uses OS-specific separators)
	srcPath := srcDir + "/" + fileName

	// Get file size first to enable resumable transfers
	fileSize, err := getRemoteFileSize(ctx, kubeCli, podName, containerName, srcPath)
	if err != nil {
		return fmt.Errorf("failed to get remote file size: %w", err)
	}

	// Initialize resume state
	state := &resumeState{
		fileSize:         fileSize,
		bytesTransferred: 0,
	}

	var lastErr error
	failedAttemptsWithoutProgress := 0
	for attempt := 1; failedAttemptsWithoutProgress < numAttempts; attempt++ {
		// Track progress before this attempt
		progressBefore := state.bytesTransferred

		// Attempt the file copy (will resume from state.bytesTransferred if > 0)
		_, lastErr = attemptResumableFileCopy(ctx, output, kubeCli, podName, containerName, srcPath, destPath, state)
		if lastErr == nil {
			log.Debug().Msgf("File copy completed successfully on attempt %d", attempt)

			// Verify integrity via MD5 checksum
			output.AppendLinef("Verifying file integrity...")
			remoteMD5, err := getRemoteFileMD5(ctx, kubeCli, podName, containerName, srcPath)
			if err != nil {
				// Don't fail the transfer, just warn
				log.Warn().Msgf("Failed to get remote MD5 for verification: %v", err)
			} else {
				localMD5, err := calculateLocalFileMD5(destPath)
				if err != nil {
					log.Warn().Msgf("Failed to calculate local MD5 for verification: %v", err)
				} else if remoteMD5 != localMD5 {
					// MD5 mismatch - file is corrupted, start fresh
					output.AppendLinef("Integrity check failed (MD5 mismatch), retrying from scratch...")
					os.Remove(destPath)
					state.bytesTransferred = 0
					lastErr = fmt.Errorf("integrity check failed: remote=%s, local=%s", remoteMD5, localMD5)
					continue
				} else {
					output.AppendLinef("Integrity verified: %s", remoteMD5[:12]+"...")
				}
			}
			return nil
		}

		// Check if progress was made - if so, reset failure counter
		if state.bytesTransferred > progressBefore {
			log.Debug().Msgf("Progress made: %d -> %d bytes, resetting failure counter", progressBefore, state.bytesTransferred)
			failedAttemptsWithoutProgress = 0
		} else {
			failedAttemptsWithoutProgress++
			log.Debug().Msgf("No progress made, failed attempts without progress: %d/%d", failedAttemptsWithoutProgress, numAttempts)
		}

		// On failure, don't delete the partial file - we'll resume from it
		if failedAttemptsWithoutProgress < numAttempts {
			if state.bytesTransferred > 0 {
				output.AppendLinef("Attempt %d failed at %s of %s, will resume...",
					attempt, humanizeFileSize(state.bytesTransferred), humanizeFileSize(fileSize))
			} else {
				output.AppendLinef("Attempt %d failed: %v, retrying...", attempt, lastErr)
			}
		}
	}

	// All attempts failed, clean up partial file
	os.Remove(destPath)
	return fmt.Errorf("file copy failed after %d attempts without progress: %w", numAttempts, lastErr)
}

// ReadFileFromPod fetches a file from a pod without using compression.
// This works in minimal containers with no shell, only requires 'tar' binary to be present (not gzip).
func ReadFileFromPod(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName string) ([]byte, error) {
	// Open a stream to read the remote file with compression disabled
	tr, closer, _, err := streamFileFromPod(ctx, kubeCli, podName, containerName, srcDir, fileName, false)
	if err != nil {
		return nil, err
	}
	defer closer()

	// Read the full payload
	return io.ReadAll(tr)
}

// checkContainerTermination checks if a container was terminated and logs the reason.
// This helps diagnose failures caused by OOM kills, evictions, or other container terminations.
func checkContainerTermination(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName string) {
	pod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		log.Debug().Msgf("Failed to check container status: %v", err)
		return
	}

	// Check ephemeral container statuses
	for _, status := range pod.Status.EphemeralContainerStatuses {
		if status.Name == containerName {
			if status.State.Terminated != nil {
				t := status.State.Terminated
				log.Debug().Msgf("Container %s was terminated: reason=%s exitCode=%d signal=%d message=%s",
					containerName, t.Reason, t.ExitCode, t.Signal, t.Message)
			} else if status.State.Waiting != nil {
				log.Debug().Msgf("Container %s is waiting: reason=%s message=%s",
					containerName, status.State.Waiting.Reason, status.State.Waiting.Message)
			}
			return
		}
	}

	// Check regular container statuses as fallback
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == containerName {
			if status.State.Terminated != nil {
				t := status.State.Terminated
				log.Debug().Msgf("Container %s was terminated: reason=%s exitCode=%d signal=%d message=%s",
					containerName, t.Reason, t.ExitCode, t.Signal, t.Message)
			}
			return
		}
	}
}

// Humanize a file size to more readable format.
func humanizeFileSize(fileSize int64) string {
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
