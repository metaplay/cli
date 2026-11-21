/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/kubeutil"
)

const testShardSQL = "INSERT INTO `Players` VALUES (1,'a'),(2,'b');\n-- Dump completed on 2026-11-21\n"

// writeTestDumps writes the dumps the export downloads: the schema and each shard's gzipped data.
func writeTestDumps(t *testing.T, localDir string, shardData [][]byte) []kubeutil.DatabaseShardConfig {
	t.Helper()
	schema := "CREATE TABLE `Players` (`Id` int) ENGINE=InnoDB DEFAULT CHARSET=latin1 COLLATE=latin1_swedish_ci;\n"
	if err := os.WriteFile(filepath.Join(localDir, exportSchemaFileName), []byte(schema), 0644); err != nil {
		t.Fatal(err)
	}
	var shards []kubeutil.DatabaseShardConfig
	for i, data := range shardData {
		if err := os.WriteFile(filepath.Join(localDir, shardDumpFileName(i)), data, 0644); err != nil {
			t.Fatal(err)
		}
		shards = append(shards, kubeutil.DatabaseShardConfig{ShardIndex: i, DatabaseName: "test_db"})
	}
	return shards
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readZipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = reader.Close() }()
	entries := map[string]string{}
	for _, f := range reader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name] = string(data)
	}
	return entries
}

func TestWriteDatabaseArchive(t *testing.T) {
	localDir := t.TempDir()
	shards := writeTestDumps(t, localDir, [][]byte{gzipped(t, testShardSQL), gzipped(t, "-- shard 1\n")})
	outputPath := filepath.Join(t.TempDir(), "archive.mdb")
	metadata := DatabaseArchiveMetadata{Version: 1, Environment: "test", DatabaseName: "test_db", NumShards: 2}

	if err := writeDatabaseArchive(context.Background(), outputPath, localDir, metadata, shards); err != nil {
		t.Fatalf("writeDatabaseArchive: %v", err)
	}

	entries := readZipEntries(t, outputPath)
	if len(entries) != 4 {
		t.Errorf("archive has entries %v, want metadata.json, schema.sql, shard_0.sql and shard_1.sql", entries)
	}
	var gotMetadata DatabaseArchiveMetadata
	if err := json.Unmarshal([]byte(entries["metadata.json"]), &gotMetadata); err != nil || gotMetadata.NumShards != 2 {
		t.Errorf("metadata.json = %q (%v)", entries["metadata.json"], err)
	}
	if !strings.Contains(entries["schema.sql"], "COLLATE=utf8mb4_bin") {
		t.Errorf("schema.sql does not have the collation fixup: %q", entries["schema.sql"])
	}
	if entries["shard_0.sql"] != testShardSQL || entries["shard_1.sql"] != "-- shard 1\n" {
		t.Errorf("shard data differs: %q, %q", entries["shard_0.sql"], entries["shard_1.sql"])
	}
	if _, err := os.Stat(filepath.Join(localDir, "archive.part")); !os.IsNotExist(err) {
		t.Errorf("temporary archive file remains: %v", err)
	}
}

// A shard dump that is cut short or corrupt fails the archive, and the output path keeps what it
// had: no archive with partial data is ever written there.
func TestWriteDatabaseArchiveRejectsBadDumps(t *testing.T) {
	full := gzipped(t, strings.Repeat(testShardSQL, 1000))
	corrupt := bytes.Clone(full)
	corrupt[len(corrupt)-5] ^= 0xff // In the gzip trailer's CRC
	cases := map[string][]byte{
		"cut short": full[:len(full)/2],
		"corrupt":   corrupt,
		"not gzip":  []byte(testShardSQL),
	}
	for name, dump := range cases {
		t.Run(name, func(t *testing.T) {
			localDir := t.TempDir()
			shards := writeTestDumps(t, localDir, [][]byte{dump})
			outputPath := filepath.Join(t.TempDir(), "archive.mdb")
			if err := os.WriteFile(outputPath, []byte("earlier archive"), 0644); err != nil {
				t.Fatal(err)
			}

			err := writeDatabaseArchive(context.Background(), outputPath, localDir, DatabaseArchiveMetadata{NumShards: 1}, shards)
			if err == nil {
				t.Fatal("writeDatabaseArchive succeeded with a bad shard dump")
			}
			if got, _ := os.ReadFile(outputPath); string(got) != "earlier archive" {
				t.Errorf("output file changed to %d bytes", len(got))
			}
			if _, err := os.Stat(filepath.Join(localDir, "archive.part")); !os.IsNotExist(err) {
				t.Errorf("temporary archive file remains: %v", err)
			}
		})
	}
}

// A cancelled export stops writing the archive, and leaves no archive or temporary file.
func TestWriteDatabaseArchiveStopsWhenCancelled(t *testing.T) {
	localDir := t.TempDir()
	shards := writeTestDumps(t, localDir, [][]byte{gzipped(t, testShardSQL)})
	outputPath := filepath.Join(t.TempDir(), "archive.mdb")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := writeDatabaseArchive(ctx, outputPath, localDir, DatabaseArchiveMetadata{NumShards: 1}, shards)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want a cancellation", err)
	}
	for _, path := range []string{outputPath, filepath.Join(localDir, "archive.part")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a cancelled write: %v", path, err)
		}
	}
}

// Through sh, the dump command's arguments are the credentials as they are, also for a password
// with shell characters.
func TestMariadbDumpCommandQuotesCredentials(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	password := `p$w'd "x" & $(id) *`
	shard := kubeutil.DatabaseShardConfig{ReadOnlyHost: "db.local", UserId: "user", Password: password, DatabaseName: "game_db"}
	out, err := exec.Command(shell, "-c", "printf '%s\\n' "+mariadbDumpCommand(shard, "--no-data", "--hex-blob")).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "mariadb-dump\n--host=db.local\n--user=user\n--password=" + password + "\n--no-data\n--hex-blob\ngame_db\n"
	if string(out) != want {
		t.Errorf("arguments = %q, want %q", out, want)
	}
}

// An output path the archive cannot be written to is found before the export: a directory or an
// existing file that cannot be opened for writing. A new or writable file is fine, and an existing
// one is not changed.
func TestCheckOutputPath(t *testing.T) {
	dir := t.TempDir()
	if err := checkOutputPath(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("checkOutputPath(directory) = %v, want an error about the directory", err)
	}
	if _, err := os.Stat("/dev/null"); err == nil {
		if err := checkOutputPath("/dev/null"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("checkOutputPath(/dev/null) = %v, want an error about a file that is not regular", err)
		}
	}
	if err := checkOutputPath(filepath.Join(dir, "new.mdb")); err != nil {
		t.Errorf("checkOutputPath(new file) = %v, want no error", err)
	}
	existing := filepath.Join(dir, "existing.mdb")
	if err := os.WriteFile(existing, []byte("earlier archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputPath(existing); err != nil {
		t.Errorf("checkOutputPath(writable file) = %v, want no error", err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "earlier archive" {
		t.Errorf("existing file changed to %q", got)
	}
	if err := os.Chmod(existing, 0o444); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(existing, os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		t.Skip("a read-only file is writable here, eg, as root")
	}
	if err := checkOutputPath(existing); err == nil {
		t.Error("checkOutputPath(read-only file) succeeded, want an error")
	}
}

// An archive that replaces a file keeps the file's permissions.
func TestWriteDatabaseArchiveKeepsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permissions")
	}
	localDir := t.TempDir()
	shards := writeTestDumps(t, localDir, [][]byte{gzipped(t, testShardSQL)})
	outputPath := filepath.Join(t.TempDir(), "archive.mdb")
	if err := os.WriteFile(outputPath, []byte("earlier archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDatabaseArchive(context.Background(), outputPath, localDir, DatabaseArchiveMetadata{NumShards: 1}, shards); err != nil {
		t.Fatalf("writeDatabaseArchive: %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %v, %v; want -rw-------", info.Mode().Perm(), err)
	}
}

// In a directory that is not writable, the archive is copied into an existing, writable output
// file, since it cannot be renamed into place.
func TestWriteDatabaseArchiveIntoReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	localDir := t.TempDir()
	shards := writeTestDumps(t, localDir, [][]byte{gzipped(t, testShardSQL)})
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "archive.mdb")
	if err := os.WriteFile(outputPath, []byte("earlier archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outputDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outputDir, 0o700) })

	if err := writeDatabaseArchive(context.Background(), outputPath, localDir, DatabaseArchiveMetadata{NumShards: 1}, shards); err != nil {
		t.Fatalf("writeDatabaseArchive: %v", err)
	}
	archive, err := zip.OpenReader(outputPath)
	if err != nil {
		t.Fatalf("output is not a zip archive: %v", err)
	}
	_ = archive.Close()
}
