/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	utilexec "k8s.io/client-go/util/exec"
)

// Short exec calls, such as a file's size or a detached command's check, are each limited to
// shortExecTimeout. A call on a connection that stalls without failing would otherwise block for as
// long as the connection lasts. Calls that keep failing, eg, on a dropped connection, are given up
// once they have failed for shortExecMaxFailingTime, so a connection that drops for a while does
// not fail an operation that may still go on fine.
var (
	shortExecTimeout         = 30 * time.Second
	shortExecMaxFailingTime  = time.Minute
	shortExecFirstRetryDelay = 200 * time.Millisecond
)

// callShortExec runs call with the time limit shortExecTimeout.
func callShortExec[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	callCtx, cancel := context.WithTimeout(ctx, shortExecTimeout)
	defer cancel()
	return call(callCtx)
}

// retryShortExec runs call, a short exec call that can be run again, with callShortExec until it
// succeeds or has failed for shortExecMaxFailingTime. An exit code of the command, a
// utilexec.CodeExitError, ends the retries at once: the command ran and would fail again. It waits
// shortExecFirstRetryDelay after the first failure, twice as long after each next one, up to 5 s.
func retryShortExec[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	start := time.Now()
	delay := shortExecFirstRetryDelay
	for {
		value, err := callShortExec(ctx, call)
		_, exited := errors.AsType[utilexec.CodeExitError](err)
		if err == nil || exited || ctx.Err() != nil || time.Since(start) >= shortExecMaxFailingTime {
			return value, err
		}
		log.Debug().Msgf("Exec call failed, retrying in %v: %v", delay, err)
		select {
		case <-ctx.Done():
			return value, err
		case <-time.After(delay):
		}
		delay = min(2*delay, 5*time.Second)
	}
}
