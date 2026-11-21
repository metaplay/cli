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
	"path/filepath"
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
	if _, err := os.Stat(outputPath + ".part"); !os.IsNotExist(err) {
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
			if _, err := os.Stat(outputPath + ".part"); !os.IsNotExist(err) {
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
	for _, path := range []string{outputPath, outputPath + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a cancelled write: %v", path, err)
		}
	}
}

// The dump command quotes the credentials, so a password with shell characters works.
func TestMariadbDumpCommandQuotesCredentials(t *testing.T) {
	shard := kubeutil.DatabaseShardConfig{ReadOnlyHost: "db.local", UserId: "user", Password: `p$w'd "x`, DatabaseName: "game_db"}
	got := mariadbDumpCommand(shard, "--no-data")
	want := `mariadb-dump --host='db.local' --user='user' --password='p$w'\''d "x' --no-data 'game_db'`
	if got != want {
		t.Errorf("mariadbDumpCommand =\n  %s\nwant\n  %s", got, want)
	}
}
