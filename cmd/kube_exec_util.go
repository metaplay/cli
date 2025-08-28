/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/rs/zerolog/log"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/util/term"
)

// IOStreams holds the IO streams for remote terminal streaming
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// execRemoteKubernetesCommand creates a SPDY executor and executes a remote command stream with proper terminal handling.
// It handles both TTY and non-TTY modes, with proper terminal state management for TTY mode.
func execRemoteKubernetesCommand(ctx context.Context, restConfig *restclient.Config, requestURL *url.URL, ioStreams IOStreams, interactive, showPressEnter bool) error {
	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", requestURL)
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %v", err)
	}

	// Helper function for common streaming logic
	streamWithLogging := func(streamOptions remotecommand.StreamOptions) error {
		if showPressEnter {
			log.Info().Msg("Press ENTER to continue..")
		}

		err := exec.StreamWithContext(ctx, streamOptions)
		log.Debug().Msgf("Stream terminated with result: %v", err)
		return err
	}

	// Handle TTY mode with proper terminal state management
	if interactive {
		ttyHandler := term.TTY{
			In:     ioStreams.In,
			Out:    ioStreams.Out,
			Raw:    true, // Enable raw mode to prevent double echo
			Parent: nil,
		}
		terminalSizeQueue := ttyHandler.MonitorSize(ttyHandler.GetSize())

		// Use TTY.Safe to properly handle terminal state
		return ttyHandler.Safe(func() error {
			streamOptions := remotecommand.StreamOptions{
				Stdin:             ioStreams.In,
				Stdout:            ioStreams.Out,
				Stderr:            nil, // In TTY mode, stderr is merged with stdout
				Tty:               true,
				TerminalSizeQueue: terminalSizeQueue,
			}
			return streamWithLogging(streamOptions)
		})
	} else {
		// Non-TTY mode - simpler handling
		streamOptions := remotecommand.StreamOptions{
			Stdin:             ioStreams.In,
			Stdout:            ioStreams.Out,
			Stderr:            ioStreams.ErrOut,
			Tty:               false,
			TerminalSizeQueue: nil,
		}
		return streamWithLogging(streamOptions)
	}
}
