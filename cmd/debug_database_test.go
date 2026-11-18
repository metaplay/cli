/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"fmt"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/kubeutil"
)

func testShards(n int) []kubeutil.DatabaseShardConfig {
	shards := make([]kubeutil.DatabaseShardConfig, n)
	for i := range shards {
		shards[i] = kubeutil.DatabaseShardConfig{ShardIndex: i, DatabaseName: fmt.Sprintf("db%d", i)}
	}
	return shards
}

func chooseShardAt(i int) shardChooser {
	return func(shards []kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		return &shards[i], nil
	}
}

func failIfChooserCalled(t *testing.T) shardChooser {
	return func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		t.Fatal("shard dialog should not be shown")
		return nil, nil
	}
}

// Regression test: the dialog's choice used to be ignored, so every pick connected to shard 0.
func TestSelectDatabaseShard_DialogChoiceIsUsed(t *testing.T) {
	shards := testShards(3)
	for want := range shards {
		got, err := selectDatabaseShard(shards, -1, chooseShardAt(want))
		if err != nil {
			t.Fatalf("pick %d: unexpected error: %v", want, err)
		}
		if got.ShardIndex != want || got.DatabaseName != shards[want].DatabaseName {
			t.Errorf("pick %d: got shard %d (%s)", want, got.ShardIndex, got.DatabaseName)
		}
	}
}

// Cases where the dialog must not be shown.
func TestSelectDatabaseShard_NoDialog(t *testing.T) {
	testCases := []struct {
		name         string
		numShards    int
		index        int
		wantIndex    int
		wantUsageErr bool
	}{
		{name: "explicit index", numShards: 3, index: 2, wantIndex: 2},
		{name: "single shard, no index", numShards: 1, index: -1, wantIndex: 0},
		{name: "index out of range", numShards: 2, index: 2, wantUsageErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectDatabaseShard(testShards(tc.numShards), tc.index, failIfChooserCalled(t))
			if tc.wantUsageErr {
				if !clierrors.IsUsageError(err) {
					t.Errorf("expected a usage error, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ShardIndex != tc.wantIndex {
				t.Errorf("got shard %d, want %d", got.ShardIndex, tc.wantIndex)
			}
		})
	}
}

func TestSelectDatabaseShard_DialogError(t *testing.T) {
	dialogErr := errors.New("canceled")
	_, err := selectDatabaseShard(testShards(2), -1, func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		return nil, dialogErr
	})
	if !errors.Is(err, dialogErr) {
		t.Errorf("expected the dialog error, got: %v", err)
	}
}
