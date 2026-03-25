/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package syncutil

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelMap_PreservesOrder(t *testing.T) {
	items := []int{10, 20, 30, 40, 50}
	results := ParallelMap(items, 5, func(n int) int {
		return n * 2
	})

	expected := []int{20, 40, 60, 80, 100}
	if len(results) != len(expected) {
		t.Fatalf("got len %d, want %d", len(results), len(expected))
	}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("results[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

func TestParallelMap_EmptySlice(t *testing.T) {
	results := ParallelMap([]int{}, 4, func(n int) int {
		t.Fatal("fn should not be called for empty slice")
		return 0
	})
	if len(results) != 0 {
		t.Fatalf("got len %d, want 0", len(results))
	}
}

func TestParallelMap_SingleItem(t *testing.T) {
	results := ParallelMap([]string{"hello"}, 4, func(s string) int {
		return len(s)
	})
	if len(results) != 1 || results[0] != 5 {
		t.Fatalf("got %v, want [5]", results)
	}
}

func TestParallelMap_TypeConversion(t *testing.T) {
	items := []int{1, 2, 3}
	results := ParallelMap(items, 2, func(n int) string {
		return fmt.Sprintf("item-%d", n)
	})
	expected := []string{"item-1", "item-2", "item-3"}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("results[%d] = %q, want %q", i, v, expected[i])
		}
	}
}

func TestParallelMap_RespectsConcurrencyLimit(t *testing.T) {
	const maxConcurrency = 3
	const numItems = 20

	var inflight atomic.Int32
	var maxInflight atomic.Int32

	items := make([]int, numItems)
	for i := range items {
		items[i] = i
	}

	ParallelMap(items, maxConcurrency, func(n int) int {
		cur := inflight.Add(1)
		// Track the peak concurrency observed.
		for {
			prev := maxInflight.Load()
			if cur <= prev || maxInflight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inflight.Add(-1)
		return n
	})

	peak := maxInflight.Load()
	if peak > int32(maxConcurrency) {
		t.Errorf("peak concurrency was %d, want at most %d", peak, maxConcurrency)
	}
	if peak == 0 {
		t.Error("peak concurrency was 0, expected at least 1")
	}
}
