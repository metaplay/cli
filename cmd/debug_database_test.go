/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/kubeutil"
)

func testShards(n int) []kubeutil.DatabaseShardConfig {
	shards := make([]kubeutil.DatabaseShardConfig, n)
	for i := range shards {
		shards[i] = kubeutil.DatabaseShardConfig{ShardIndex: i, DatabaseName: "db" + string(rune('0'+i))}
	}
	return shards
}

func chooseShardAt(i int) func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
	return func(shards []kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		return &shards[i], nil
	}
}

func failIfChooserCalled(t *testing.T) func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
	return func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		t.Fatal("shard dialog should not be shown")
		return nil, nil
	}
}

// Regression test: the dialog's choice used to be ignored, so every pick connected to shard 0.
func TestSelectDatabaseShard_DialogChoiceIsUsed(t *testing.T) {
	shards := testShards(3)
	for want := range shards {
		got, err := selectDatabaseShard(shards, false, 0, chooseShardAt(want))
		if err != nil {
			t.Fatalf("pick %d: unexpected error: %v", want, err)
		}
		if got.ShardIndex != want || got.DatabaseName != shards[want].DatabaseName {
			t.Errorf("pick %d: got shard %d (%s)", want, got.ShardIndex, got.DatabaseName)
		}
	}
}

func TestSelectDatabaseShard_ExplicitIndex(t *testing.T) {
	shards := testShards(3)
	got, err := selectDatabaseShard(shards, true, 2, failIfChooserCalled(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ShardIndex != 2 {
		t.Errorf("got shard %d, want 2", got.ShardIndex)
	}
}

func TestSelectDatabaseShard_SingleShardSkipsDialog(t *testing.T) {
	got, err := selectDatabaseShard(testShards(1), false, 0, failIfChooserCalled(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ShardIndex != 0 {
		t.Errorf("got shard %d, want 0", got.ShardIndex)
	}
}

func TestSelectDatabaseShard_IndexOutOfRange(t *testing.T) {
	_, err := selectDatabaseShard(testShards(2), true, 2, failIfChooserCalled(t))
	if err == nil {
		t.Fatal("expected an error for index 2 with 2 shards")
	}
	if !clierrors.IsUsageError(err) {
		t.Errorf("expected a usage error, got: %v", err)
	}
}

func TestSelectDatabaseShard_NoShards(t *testing.T) {
	_, err := selectDatabaseShard(nil, false, 0, failIfChooserCalled(t))
	if err == nil {
		t.Fatal("expected an error when no shards are configured")
	}
}

func TestSelectDatabaseShard_DialogError(t *testing.T) {
	dialogErr := errors.New("canceled")
	_, err := selectDatabaseShard(testShards(2), false, 0, func([]kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
		return nil, dialogErr
	})
	if !errors.Is(err, dialogErr) {
		t.Errorf("expected the dialog error, got: %v", err)
	}
}
