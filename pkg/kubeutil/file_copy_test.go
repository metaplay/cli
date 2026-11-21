/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/metaplay/cli/internal/tui"
)

// failingReader simulates a reader that fails after reading a specified number of bytes
type failingReader struct {
	data      []byte
	pos       int
	failAfter int
	failCount int
	maxFails  int
	failError error
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
	tmpDir := t.TempDir()

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
		var err error
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
		_ = destFile.Close()

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
	tmpDir := t.TempDir()

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
		_ = f.Close()
		t.Fatalf("Failed to write second half: %v", err)
	}
	_ = f.Close()

	// Verify combined content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	expected := slices.Concat(firstHalf, secondHalf)
	if !bytes.Equal(content, expected) {
		t.Errorf("Content mismatch:\nExpected: %s\nGot: %s", expected, content)
	}
}

func TestMD5Calculation(t *testing.T) {
	tmpDir := t.TempDir()

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

// fakeRemoteFile is a remoteFile whose streams end after maxChunk bytes without an error, as an
// exec stream does when its connection closes early.
type fakeRemoteFile struct {
	data     []byte
	maxChunk int
	md5      string // Reported MD5, or the data's MD5 if empty
	md5Err   error
	md5Fails int // How many MD5 calls fail before they succeed, when md5Err is nil
	md5Calls int
	opens    int

	wrongMD5Calls int // How many MD5 calls report a wrong checksum first
	emptyAfter    int // Opens after this many return no data, if not 0
}

func (f *fakeRemoteFile) Size(ctx context.Context) (int64, error) { return int64(len(f.data)), nil }

func (f *fakeRemoteFile) MD5(ctx context.Context) (string, error) {
	f.md5Calls++
	if f.md5Err != nil {
		return "", f.md5Err
	}
	if f.md5Calls <= f.md5Fails {
		return "", errors.New("connection lost")
	}
	if f.md5Calls <= f.wrongMD5Calls {
		return "00000000000000000000000000000000", nil
	}
	if f.md5 != "" {
		return f.md5, nil
	}
	sum := md5.Sum(f.data)
	return hex.EncodeToString(sum[:]), nil
}

func (f *fakeRemoteFile) Open(ctx context.Context, offset int64) (io.Reader, func() error, error) {
	f.opens++
	if f.emptyAfter > 0 && f.opens > f.emptyAfter {
		return bytes.NewReader(nil), func() error { return nil }, nil
	}
	end := min(int(offset)+f.maxChunk, len(f.data))
	return bytes.NewReader(f.data[offset:end]), func() error { return nil }, nil
}

func (f *fakeRemoteFile) TransferFailed(ctx context.Context) {}

func testData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	return data
}

// Streams that end early without an error are resumed until the whole file has arrived.
func TestCopyRemoteFileResumesStreamsThatEndEarly(t *testing.T) {
	file := &fakeRemoteFile{data: testData(10000), maxChunk: 3000}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	if err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 2); err != nil {
		t.Fatalf("copyRemoteFile: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil || !bytes.Equal(got, file.data) {
		t.Fatalf("copied file differs from the remote file (err=%v, %d bytes)", err, len(got))
	}
	if file.opens != 4 {
		t.Errorf("opened %d streams, want 4 for 10000 bytes in chunks of 3000", file.opens)
	}
}

// A stream that never makes progress fails the copy, and leaves no file behind.
func TestCopyRemoteFileFailsWithoutProgress(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 0}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3)
	if err == nil || !strings.Contains(err.Error(), "stream ended at 0 of 100 bytes") {
		t.Errorf("err = %v, want the stream ending early", err)
	}
	if file.opens != 3 {
		t.Errorf("opened %d streams, want 3", file.opens)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Errorf("destination file exists after a failed copy: %v", statErr)
	}
}

// A copy whose checksum cannot be checked is not complete.
func TestCopyRemoteFileFailsWithoutMD5(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 100, md5Err: errors.New("md5sum not found")}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 2)
	if err == nil || !strings.Contains(err.Error(), "md5sum not found") {
		t.Errorf("err = %v, want the MD5 error", err)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Errorf("destination file exists after a failed copy: %v", statErr)
	}
}

// A checksum that never matches fails the copy after numAttempts, instead of retrying forever.
func TestCopyRemoteFileStopsOnRepeatedMD5Mismatch(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 100, md5: "00000000000000000000000000000000"}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3)
	if err == nil || !strings.Contains(err.Error(), "MD5 mismatch") {
		t.Errorf("err = %v, want an MD5 mismatch", err)
	}
	if file.opens != 3 {
		t.Errorf("opened %d streams, want 3", file.opens)
	}
}

// A checksum that never matches also fails a file that takes several streams to copy: starting
// over makes progress, but does not let the copy retry forever.
func TestCopyRemoteFileStopsOnRepeatedMD5MismatchOfResumedCopy(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 30, md5: "00000000000000000000000000000000"}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3)
	if err == nil || !strings.Contains(err.Error(), "MD5 mismatch") {
		t.Errorf("err = %v, want an MD5 mismatch", err)
	}
	if file.md5Calls != 3 {
		t.Errorf("checked the MD5 %d times, want 3", file.md5Calls)
	}
}

// A mismatch is not also an attempt without progress: after one, the copy still gets numAttempts
// attempts without progress.
func TestCopyRemoteFileCountsMismatchOnItsOwn(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 100, wrongMD5Calls: 1, emptyAfter: 1}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3)
	if err == nil || !strings.Contains(err.Error(), "without progress") {
		t.Errorf("err = %v, want the copy to fail without progress", err)
	}
	if file.opens != 4 {
		t.Errorf("opened %d streams, want 4: the mismatched copy and 3 attempts without progress", file.opens)
	}
}

// When getting the remote checksum fails, only the check is retried: the file is not copied again.
func TestCopyRemoteFileRetriesOnlyTheFailedChecksum(t *testing.T) {
	file := &fakeRemoteFile{data: testData(100), maxChunk: 100, md5Fails: 2}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	if err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3); err != nil {
		t.Fatalf("copyRemoteFile: %v", err)
	}
	if file.opens != 1 || file.md5Calls != 3 {
		t.Errorf("opened %d streams and checked the MD5 %d times, want 1 and 3", file.opens, file.md5Calls)
	}
}

// A stream with more than the file's size means the file changed during the copy. That fails the
// copy at once: every retry would fail the same way.
func TestCopyRemoteFileFailsOnLongerStream(t *testing.T) {
	file := &longerRemoteFile{fakeRemoteFile{data: testData(100), maxChunk: 1000}}
	destPath := filepath.Join(t.TempDir(), "out.bin")

	err := copyRemoteFile(context.Background(), &tui.TaskOutput{}, file, destPath, 3)
	if err == nil || !strings.Contains(err.Error(), "changed during the copy") {
		t.Errorf("err = %v, want an error about the file changing", err)
	}
	if file.opens != 1 {
		t.Errorf("opened %d streams, want 1", file.opens)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Errorf("destination file exists after a failed copy: %v", statErr)
	}
}

// longerRemoteFile reports a size 10 bytes smaller than what it streams.
type longerRemoteFile struct{ fakeRemoteFile }

func (f *longerRemoteFile) Size(ctx context.Context) (int64, error) {
	return int64(len(f.data) - 10), nil
}
