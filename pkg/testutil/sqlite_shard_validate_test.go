/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package testutil

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// createShardDB creates a SQLite file at path with the given tables, each populated with the given row
// count. Tables not present in the map are simply not created (used to model inactive shards).
func createShardDB(t *testing.T, path string, tableCounts map[string]int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for table, count := range tableCounts {
		_, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INTEGER PRIMARY KEY)", quoteSQLiteIdent(table)))
		require.NoError(t, err)
		for range count {
			_, err := db.Exec(fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", quoteSQLiteIdent(table)))
			require.NoError(t, err)
		}
	}
}

// writeShardLayout writes shard files Shardy-0.db .. into dir from the given per-shard table->count maps.
// A nil entry means no file is created for that shard (models a removed inactive shard).
func writeShardLayout(t *testing.T, dir string, shards []map[string]int) {
	t.Helper()
	for shardNdx, counts := range shards {
		if counts == nil {
			continue
		}
		createShardDB(t, filepath.Join(dir, fmt.Sprintf("Shardy-%d.db", shardNdx)), counts)
	}
}

func TestCompareAgainstPriorSteps(t *testing.T) {
	tables := []string{"Players", "AuthEntries"}

	stepFourActive := ShardingStepState{
		NumActiveShards: 4,
		ItemCounts: map[string][]int{
			"Players":     {3, 1, 2, 0},
			"AuthEntries": {2, 4, 0, 1},
		},
	}

	t.Run("same shard count matching per-shard counts passes", func(t *testing.T) {
		current := map[string][]int{
			"Players":     {3, 1, 2, 0},
			"AuthEntries": {2, 4, 0, 1},
		}
		require.NoError(t, compareAgainstPriorSteps(tables, 4, current, []ShardingStepState{stepFourActive}))
	})

	t.Run("same shard count with reordered counts fails", func(t *testing.T) {
		// Same totals, but a different per-shard distribution must fail for equal shard counts.
		current := map[string][]int{
			"Players":     {0, 2, 1, 3},
			"AuthEntries": {1, 0, 4, 2},
		}
		err := compareAgainstPriorSteps(tables, 4, current, []ShardingStepState{stepFourActive})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Players")
	})

	t.Run("different shard count with matching sums passes", func(t *testing.T) {
		// Reshard to 1 active shard: only totals need to match.
		current := map[string][]int{
			"Players":     {6},
			"AuthEntries": {7},
		}
		require.NoError(t, compareAgainstPriorSteps(tables, 1, current, []ShardingStepState{stepFourActive}))
	})

	t.Run("different shard count with mismatched sums fails", func(t *testing.T) {
		current := map[string][]int{
			"Players":     {6},
			"AuthEntries": {99}, // wrong total
		}
		err := compareAgainstPriorSteps(tables, 1, current, []ShardingStepState{stepFourActive})
		require.Error(t, err)
		require.Contains(t, err.Error(), "AuthEntries")
	})

	t.Run("compares against all prior steps", func(t *testing.T) {
		stepOneActive := ShardingStepState{
			NumActiveShards: 1,
			ItemCounts: map[string][]int{
				"Players":     {6},
				"AuthEntries": {7},
			},
		}
		// Back to 4 active shards: must match the 4-shard step per-shard, and the 1-shard step by sum.
		current := map[string][]int{
			"Players":     {3, 1, 2, 0},
			"AuthEntries": {2, 4, 0, 1},
		}
		require.NoError(t, compareAgainstPriorSteps(tables, 4, current, []ShardingStepState{stepFourActive, stepOneActive}))

		// A distribution that matches the sum but not the original 4-shard layout must fail.
		bad := map[string][]int{
			"Players":     {6, 0, 0, 0},
			"AuthEntries": {7, 0, 0, 0},
		}
		require.Error(t, compareAgainstPriorSteps(tables, 4, bad, []ShardingStepState{stepFourActive, stepOneActive}))
	})

	t.Run("no prior steps always passes", func(t *testing.T) {
		current := map[string][]int{"Players": {1, 2, 3, 4}, "AuthEntries": {0, 0, 0, 0}}
		require.NoError(t, compareAgainstPriorSteps(tables, 4, current, nil))
	})
}

func TestCollectShardItemCounts(t *testing.T) {
	tables := []string{"Players", "AuthEntries"}

	t.Run("counts tables across active shards", func(t *testing.T) {
		dir := t.TempDir()
		writeShardLayout(t, dir, []map[string]int{
			{"Players": 3, "AuthEntries": 2},
			{"Players": 1, "AuthEntries": 4},
			{"Players": 2, "AuthEntries": 0},
			{"Players": 0, "AuthEntries": 1},
		})

		counts, err := collectShardItemCounts(dir, tables, 4, 4)
		require.NoError(t, err)
		require.Equal(t, []int{3, 1, 2, 0}, counts["Players"])
		require.Equal(t, []int{2, 4, 0, 1}, counts["AuthEntries"])
	})

	t.Run("inactive shards must not have the tables (missing file ok)", func(t *testing.T) {
		dir := t.TempDir()
		writeShardLayout(t, dir, []map[string]int{
			{"Players": 6, "AuthEntries": 7},
			nil, // Shardy-1.db absent => treated as no tables
			nil,
			nil,
		})

		counts, err := collectShardItemCounts(dir, tables, 4, 1)
		require.NoError(t, err)
		require.Equal(t, []int{6}, counts["Players"])
		require.Equal(t, []int{7}, counts["AuthEntries"])
	})

	t.Run("inactive shard that still has a table fails", func(t *testing.T) {
		dir := t.TempDir()
		writeShardLayout(t, dir, []map[string]int{
			{"Players": 6, "AuthEntries": 7},
			{"Players": 1}, // shard 1 is inactive but still has a table => error
			nil,
			nil,
		})

		_, err := collectShardItemCounts(dir, tables, 4, 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "should not exist")
	})

	t.Run("active shard missing a table fails", func(t *testing.T) {
		dir := t.TempDir()
		writeShardLayout(t, dir, []map[string]int{
			{"Players": 3}, // missing AuthEntries on an active shard => error
			{"Players": 1, "AuthEntries": 4},
			{"Players": 2, "AuthEntries": 0},
			{"Players": 0, "AuthEntries": 1},
		})

		_, err := collectShardItemCounts(dir, tables, 4, 4)
		require.Error(t, err)
		require.Contains(t, err.Error(), "should exist")
	})
}

// TestValidateShardingSequence simulates the full 4 -> 1 -> 2 -> 4 resharding sequence with consistent
// (deterministic) per-shard distribution, mirroring how the real test validates each step against all
// prior steps.
func TestValidateShardingSequence(t *testing.T) {
	tables := []string{"Players", "AuthEntries"}
	const numShards = 4

	// Deterministic per-shard distribution for the 4-active layout.
	fourShard := []map[string]int{
		{"Players": 3, "AuthEntries": 2},
		{"Players": 1, "AuthEntries": 4},
		{"Players": 2, "AuthEntries": 0},
		{"Players": 0, "AuthEntries": 1},
	}
	// 2-active layout: shards 0..1 hold all rows (sums preserved); shards 2..3 inactive.
	twoShard := []map[string]int{
		{"Players": 4, "AuthEntries": 3},
		{"Players": 2, "AuthEntries": 4},
		nil,
		nil,
	}

	var steps []ShardingStepState

	// Step 0: initial 4-active state.
	dir := t.TempDir()
	writeShardLayout(t, dir, fourShard)
	state, err := ValidateShardingStep(dir, tables, numShards, 4, steps)
	require.NoError(t, err)
	steps = append(steps, state)

	// Step 1: reshard to 1 active (sums must match the 4-active step).
	dir = t.TempDir()
	writeShardLayout(t, dir, []map[string]int{
		{"Players": 6, "AuthEntries": 7},
		nil, nil, nil,
	})
	state, err = ValidateShardingStep(dir, tables, numShards, 1, steps)
	require.NoError(t, err)
	steps = append(steps, state)

	// Step 2: reshard to 2 active (sums must match prior steps).
	dir = t.TempDir()
	writeShardLayout(t, dir, twoShard)
	state, err = ValidateShardingStep(dir, tables, numShards, 2, steps)
	require.NoError(t, err)
	steps = append(steps, state)

	// Step 3: reshard back to 4 active (must reproduce the exact original 4-shard layout).
	dir = t.TempDir()
	writeShardLayout(t, dir, fourShard)
	state, err = ValidateShardingStep(dir, tables, numShards, 4, steps)
	require.NoError(t, err)
	steps = append(steps, state)

	require.Len(t, steps, 4)
}

// TestValidateShardingSequenceDetectsLoss ensures that a resharding step that loses rows is detected.
func TestValidateShardingSequenceDetectsLoss(t *testing.T) {
	tables := []string{"Players"}
	const numShards = 4

	var steps []ShardingStepState

	dir := t.TempDir()
	writeShardLayout(t, dir, []map[string]int{
		{"Players": 3}, {"Players": 1}, {"Players": 2}, {"Players": 0},
	})
	state, err := ValidateShardingStep(dir, tables, numShards, 4, steps)
	require.NoError(t, err)
	steps = append(steps, state)

	// Reshard to 1 active but drop a row (total 5 instead of 6) => must fail.
	dir = t.TempDir()
	writeShardLayout(t, dir, []map[string]int{
		{"Players": 5}, nil, nil, nil,
	})
	_, err = ValidateShardingStep(dir, tables, numShards, 1, steps)
	require.Error(t, err)
}

func TestSqliteReadOnlyDSN(t *testing.T) {
	// Ensures the DSN has the expected read-only/immutable query parameters and a leading slash.
	dsn := sqliteReadOnlyDSN(filepath.Join("some", "dir", "Shardy-0.db"))
	require.Contains(t, dsn, "mode=ro")
	require.Contains(t, dsn, "immutable=1")
	require.True(t, strings.HasPrefix(dsn, "file:"))

	// A path containing a space must be percent-encoded (not left as a raw space).
	spaced := sqliteReadOnlyDSN(filepath.Join("dir with space", "Shardy-0.db"))
	require.NotContains(t, spaced, " ")
	require.Contains(t, spaced, "%20")
}

// TestCollectShardItemCountsWithSpaceInPath guards against malformed DSNs when the shard directory path
// contains a space (e.g. a Windows user profile or temp directory with a space).
func TestCollectShardItemCountsWithSpaceInPath(t *testing.T) {
	tables := []string{"Players"}
	dir := filepath.Join(t.TempDir(), "shard dir with spaces")
	require.NoError(t, os.MkdirAll(dir, 0o777))

	writeShardLayout(t, dir, []map[string]int{
		{"Players": 3}, {"Players": 1}, nil, nil,
	})

	counts, err := collectShardItemCounts(dir, tables, 4, 2)
	require.NoError(t, err)
	require.Equal(t, []int{3, 1}, counts["Players"])
}
