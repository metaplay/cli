/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package syncutil

import "sync"

// ParallelMap applies fn to each element of items concurrently, with at most
// maxConcurrency goroutines in flight at a time. It returns a result slice
// with the same length and ordering as items.
func ParallelMap[In, Out any](items []In, maxConcurrency int, fn func(In) Out) []Out {
	results := make([]Out, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)
	for i, item := range items {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results[i] = fn(item)
		})
	}
	wg.Wait()
	return results
}
