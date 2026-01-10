/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

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
		pt.progressOutput.AppendLinef("Copy progress: %d%% (%s / %s)", percentComplete, humanizeFileSize(pt.numProcessed), humanizeFileSize(pt.totalSize))
	}

	return n, nil
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
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: outStream,
			Stderr: os.Stderr,
			Tty:    false,
		})
		if err != nil {
			log.Error().Msgf("Error streaming from pod: %v", err)
			outStream.CloseWithError(err)
		} else {
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

// attemptFileCopy performs a single file copy attempt
func attemptFileCopy(ctx context.Context, output *tui.TaskOutput, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName, destPath string) error {
	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destFile.Close()

	// Copy file from pod with compression enabled for faster copying.
	tr, closer, fileSize, err := streamFileFromPod(ctx, kubeCli, podName, containerName, srcDir, fileName, true)
	if err != nil {
		return err
	}
	defer closer()

	output.SetHeaderLines([]string{
		fmt.Sprintf("File size: %s", humanizeFileSize(fileSize)),
	})

	// Determine update interval: faster in interactive mode, slower in non-interactive mode
	interval := map[bool]time.Duration{
		true:  time.Second / 5,
		false: time.Second,
	}[tui.IsInteractiveMode()]

	// Create progress tracker for the file copying.
	progressTracker := &ioProgressTracker{
		outWriter:         destFile,
		progressOutput:    output,
		totalSize:         fileSize,
		minUpdateInterval: interval,
		lastUpdateTime:    time.Now(),
	}

	// Copy the file & track progress.
	if _, err := io.Copy(progressTracker, tr); err != nil {
		output.AppendLinef("Failed to copy file contents: %v (copied %d out of %d bytes)", err, progressTracker.numProcessed, fileSize)
		return fmt.Errorf("failed to copy file contents: %v", err)
	}
	return nil
}

// copyFileFromDebugPod copies a file from a pod using a tar pipe, similar to how kubectl cp works internally.
// Only works with the ephemeral debug container as this requires shell on the target pod, which the game server
// pods don't have. Use numAttempts > 1 to retry the copy operation in case of failure.
func CopyFileFromDebugPod(ctx context.Context, output *tui.TaskOutput, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName, destPath string, numAttempts int) error {
	if numAttempts < 1 {
		return fmt.Errorf("numAttempts must be at least 1")
	}

	// Create the destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	var lastErr error
	for attempt := 1; attempt <= numAttempts; attempt++ {
		// Attempt the file copy
		lastErr = attemptFileCopy(ctx, output, kubeCli, podName, containerName, srcDir, fileName, destPath)
		if lastErr == nil {
			log.Debug().Msgf("File copy completed successfully on attempt %d", attempt)
			return nil
		}

		// Remove output file on failures
		os.Remove(destPath)

		if attempt < numAttempts {
			output.AppendLinef("Attempt %d failed: %v, retrying...", attempt, lastErr)
		}
	}

	return fmt.Errorf("file copy failed after %d attempts: %w", numAttempts, lastErr)
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
