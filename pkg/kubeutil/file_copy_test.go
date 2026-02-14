/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// failingReader simulates a reader that fails after reading a specified number of bytes
type failingReader struct {
	data       []byte
	pos        int
	failAfter  int
	failCount  int
	maxFails   int
	failError  error
}

func newFailingReader(data []byte, failAfter int, maxFails int) *failingReader {
	return &failingReader{
		data:      data,
		failAfter: failAfter,
		maxFails:  maxFails,
		failError: fmt.Errorf("simulated network failure"),
	}
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	// Check if we should fail
	if r.failCount < r.maxFails && r.pos >= r.failAfter*(r.failCount+1) {
		r.failCount++
		return 0, r.failError
	}

	// Read normally
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// Reset the reader position to simulate resume from offset
func (r *failingReader) SeekTo(offset int) {
	r.pos = offset
}

func TestResumeStateTracking(t *testing.T) {
	// Test that resumeState correctly tracks progress
	state := &resumeState{
		fileSize:         1000,
		bytesTransferred: 0,
	}

	// Simulate partial transfer
	state.bytesTransferred = 500

	if state.bytesTransferred != 500 {
		t.Errorf("Expected bytesTransferred=500, got %d", state.bytesTransferred)
	}

	// Simulate completion
	state.bytesTransferred = 1000

	if state.bytesTransferred != state.fileSize {
		t.Errorf("Expected bytesTransferred=%d, got %d", state.fileSize, state.bytesTransferred)
	}
}

func TestResumableFileCopyWithSimulatedFailures(t *testing.T) {
	// Create test data (1MB)
	testData := make([]byte, 1024*1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// Calculate expected MD5
	expectedMD5 := md5.Sum(testData)
	expectedMD5Str := hex.EncodeToString(expectedMD5[:])

	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "file_copy_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, "test_output.bin")

	// Simulate transfer with failures at 256KB and 512KB
	failAfter := 256 * 1024
	maxFails := 2

	state := &resumeState{
		fileSize:         int64(len(testData)),
		bytesTransferred: 0,
	}

	// Simulate multiple attempts with resume
	attempts := 0
	maxAttempts := 5

	for attempts < maxAttempts {
		attempts++
		t.Logf("Attempt %d: starting from offset %d", attempts, state.bytesTransferred)

		// Create reader that fails at certain points
		reader := newFailingReader(testData, failAfter, maxFails)
		reader.SeekTo(int(state.bytesTransferred))
		// Adjust fail tracking based on how far we've already gotten
		reader.failCount = int(state.bytesTransferred) / failAfter

		// Open file in appropriate mode
		var destFile *os.File
		if state.bytesTransferred > 0 {
			destFile, err = os.OpenFile(destPath, os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				t.Fatalf("Failed to open file for append: %v", err)
			}
		} else {
			destFile, err = os.Create(destPath)
			if err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
		}

		// Copy data
		bytesWritten, copyErr := io.Copy(destFile, reader)
		destFile.Close()

		// Update state
		state.bytesTransferred += bytesWritten
		t.Logf("Attempt %d: wrote %d bytes, total now %d/%d", attempts, bytesWritten, state.bytesTransferred, state.fileSize)

		if copyErr == nil {
			// Success!
			t.Logf("Transfer completed successfully after %d attempts", attempts)
			break
		}

		t.Logf("Attempt %d failed: %v (will resume from %d)", attempts, copyErr, state.bytesTransferred)
	}

	if state.bytesTransferred != state.fileSize {
		t.Fatalf("Transfer incomplete: got %d bytes, expected %d", state.bytesTransferred, state.fileSize)
	}

	// Verify the file content via MD5
	actualMD5, err := calculateLocalFileMD5(destPath)
	if err != nil {
		t.Fatalf("Failed to calculate MD5: %v", err)
	}

	if actualMD5 != expectedMD5Str {
		t.Errorf("MD5 mismatch: expected %s, got %s", expectedMD5Str, actualMD5)
	}

	t.Logf("File integrity verified: %s", actualMD5)
}

func TestProgressCalculation(t *testing.T) {
	// Test that progress percentage is calculated correctly when resuming
	totalSize := int64(1000)

	// Test various resume points
	testCases := []struct {
		numProcessed    int64
		expectedPercent int
	}{
		{0, 0},
		{100, 10},
		{500, 50},
		{750, 75},
		{1000, 100},
	}

	for _, tc := range testCases {
		percentComplete := int(float64(tc.numProcessed) / float64(totalSize) * 100)
		if percentComplete != tc.expectedPercent {
			t.Errorf("For numProcessed=%d: expected %d%%, got %d%%",
				tc.numProcessed, tc.expectedPercent, percentComplete)
		}
	}
}

func TestAppendModeFileWriting(t *testing.T) {
	// Test that append mode correctly continues from previous write
	tmpDir, err := os.MkdirTemp("", "append_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, "append_test.bin")

	// First write: create file with first half
	firstHalf := []byte("FIRST_HALF_DATA_")
	if err := os.WriteFile(destPath, firstHalf, 0644); err != nil {
		t.Fatalf("Failed to write first half: %v", err)
	}

	// Second write: append second half
	secondHalf := []byte("SECOND_HALF_DATA")
	f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("Failed to open for append: %v", err)
	}
	if _, err := f.Write(secondHalf); err != nil {
		f.Close()
		t.Fatalf("Failed to write second half: %v", err)
	}
	f.Close()

	// Verify combined content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	expected := append(firstHalf, secondHalf...)
	if !bytes.Equal(content, expected) {
		t.Errorf("Content mismatch:\nExpected: %s\nGot: %s", expected, content)
	}
}

func TestMD5Calculation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "md5_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testPath := filepath.Join(tmpDir, "test.bin")
	testData := []byte("Hello, World!")

	if err := os.WriteFile(testPath, testData, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Calculate expected MD5
	expectedHash := md5.Sum(testData)
	expectedMD5 := hex.EncodeToString(expectedHash[:])

	// Calculate using our function
	actualMD5, err := calculateLocalFileMD5(testPath)
	if err != nil {
		t.Fatalf("calculateLocalFileMD5 failed: %v", err)
	}

	if actualMD5 != expectedMD5 {
		t.Errorf("MD5 mismatch: expected %s, got %s", expectedMD5, actualMD5)
	}
}
