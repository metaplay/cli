/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package testutil

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (CGO-free), registered as "sqlite"
)

// DatabaseReshardingTables is the set of database tables whose row counts are validated across resharding
// steps. It mirrors the legacy Scripts/legacy-tests.py table list.
//
// MetaInfo is deliberately excluded because it grows monotonically (a row is appended on each server
// startup) and therefore can't be compared for equality step-to-step. The list is an explicit allow-list
// for fidelity with the legacy test; it needs to be kept in sync if the SDK schema changes.
var DatabaseReshardingTables = []string{
	"GlobalStates",
	"AuditLogEvents",
	"AuthEntries",
	"Players",
	"PlayerNameSearches",
	"PlayerDeletionRecords",
	"PlayerEventLogSegments",
	"InAppPurchases",
	"DatabaseScanCoordinators",
	"DatabaseScanWorkers",
	"StaticGameConfigs",
	"SegmentEstimates",
}

// ShardingStepState captures the per-table row counts observed for one resharding step, keyed by table
// name. Each value is the list of row counts across the active shards (index = shard number). It is the
// Go equivalent of the legacy ShardingStepState.
type ShardingStepState struct {
	NumActiveShards int
	ItemCounts      map[string][]int
}

// shardTableInfo holds the existence and row count of a single table within a single shard file.
type shardTableInfo struct {
	exists bool
	count  int
}

// sqliteReadOnlyDSN builds a read-only SQLite connection string for the given shard file path. The file
// is opened immutable so SQLite neither takes locks nor creates -wal/-shm side files (safe because the
// server has already exited and the file is static).
func sqliteReadOnlyDSN(dbPath string) string {
	// Build a SQLite "file:" URI. SQLite URIs use forward slashes and expect a leading slash before a
	// Windows drive letter (e.g. file:///C:/path/Shardy-0.db). Using net/url ensures that characters such
	// as spaces in the path are percent-encoded correctly.
	p := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&immutable=1"}
	return u.String()
}

// countTablesInShard opens a single SQLite shard file read-only and, for each requested table, reports
// whether the table exists and (if so) its row count. A missing shard file is treated as having none of
// the tables (mirroring the legacy behavior, where sqlite3.connect() on a missing file yields an empty
// database with no application tables).
func countTablesInShard(dbPath string, tables []string) (map[string]shardTableInfo, error) {
	result := make(map[string]shardTableInfo, len(tables))

	// A missing shard file means none of the application tables exist.
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		for _, table := range tables {
			result[table] = shardTableInfo{exists: false}
		}
		return result, nil
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite shard %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range tables {
		// Check table existence via sqlite_master.
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			result[table] = shardTableInfo{exists: false}
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to query existence of table %s in %s: %w", table, dbPath, err)
		}

		// Table exists: count its rows.
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteSQLiteIdent(table)).Scan(&count); err != nil {
			return nil, fmt.Errorf("failed to count rows of table %s in %s: %w", table, dbPath, err)
		}
		result[table] = shardTableInfo{exists: true, count: count}
	}

	return result, nil
}

// quoteSQLiteIdent quotes a SQLite identifier (table name) by wrapping it in double quotes and escaping
// any embedded double quotes. The table names come from a fixed allow-list, but quoting is good practice.
func quoteSQLiteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// collectShardItemCounts reads the row counts of the given tables across all shard files in shardDir
// (Shardy-0.db .. Shardy-{numShards-1}.db) and validates that each table exists exactly on the active
// shards (shard index < numActiveShards). It returns the per-table counts indexed by active shard.
func collectShardItemCounts(shardDir string, tables []string, numShards, numActiveShards int) (map[string][]int, error) {
	itemCounts := make(map[string][]int, len(tables))
	for _, table := range tables {
		itemCounts[table] = make([]int, numActiveShards)
	}

	for shardNdx := range numShards {
		dbPath := filepath.Join(shardDir, fmt.Sprintf("Shardy-%d.db", shardNdx))
		info, err := countTablesInShard(dbPath, tables)
		if err != nil {
			return nil, err
		}

		shouldExist := shardNdx < numActiveShards
		for _, table := range tables {
			ti := info[table]

			// Validate table existence on the shard: it must exist iff the shard is active.
			if ti.exists != shouldExist {
				if shouldExist {
					return nil, fmt.Errorf("database shard #%d table %s should exist, but doesn't", shardNdx, table)
				}
				return nil, fmt.Errorf("database shard #%d table %s should not exist, but does (with %d items)", shardNdx, table, ti.count)
			}

			// Record counts for active shards only.
			if shouldExist {
				itemCounts[table][shardNdx] = ti.count
			}
		}
	}

	return itemCounts, nil
}

// compareAgainstPriorSteps checks the freshly collected item counts against every prior resharding step:
//   - when the active shard counts match, the per-shard counts must be identical;
//   - otherwise, the per-table totals (summed across active shards) must match.
//
// This is the pure comparison logic, separated from file I/O for straightforward unit testing.
func compareAgainstPriorSteps(tables []string, numActiveShards int, itemCounts map[string][]int, priorSteps []ShardingStepState) error {
	for _, prev := range priorSteps {
		if numActiveShards == prev.NumActiveShards {
			// Same shard count: every shard's counts must match exactly.
			for _, table := range tables {
				got := itemCounts[table]
				expected := prev.ItemCounts[table]
				if !slices.Equal(got, expected) {
					return fmt.Errorf("mismatched shard states for table '%s': got %v, expecting %v", table, got, expected)
				}
			}
		} else {
			// Different shard count: only the totals need to match.
			for _, table := range tables {
				got := sumInts(itemCounts[table])
				expected := sumInts(prev.ItemCounts[table])
				if got != expected {
					return fmt.Errorf("mismatched shard states for table '%s': got %d, expecting %d", table, got, expected)
				}
			}
		}
	}
	return nil
}

// ValidateShardingStep reads the current shard state from shardDir, validates table existence per shard,
// compares the row counts against all prior steps, and returns the new step state (to be appended to the
// running list of prior steps by the caller). It is a direct port of the legacy
// DatabaseShardingValidationTask.
func ValidateShardingStep(shardDir string, tables []string, numShards, numActiveShards int, priorSteps []ShardingStepState) (ShardingStepState, error) {
	itemCounts, err := collectShardItemCounts(shardDir, tables, numShards, numActiveShards)
	if err != nil {
		return ShardingStepState{}, err
	}

	if err := compareAgainstPriorSteps(tables, numActiveShards, itemCounts, priorSteps); err != nil {
		return ShardingStepState{}, err
	}

	return ShardingStepState{NumActiveShards: numActiveShards, ItemCounts: itemCounts}, nil
}

// sumInts returns the sum of the given integers.
func sumInts(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
