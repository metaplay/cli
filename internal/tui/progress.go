/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// RunWithProgressBar executes work while displaying a progress bar.
// The work function receives an update callback to report progress (downloaded, total bytes).
// In interactive mode: spinner + progress on one line, updated via \r.
// In non-interactive mode: logs at start and completion.
func RunWithProgressBar(label string, work func(update func(current, total int64)) error) error {
	start := time.Now()

	if !isInteractiveMode {
		log.Info().Msgf("%s...", label)
	}

	var lastCurrent, lastTotal int64
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIdx := 0

	update := func(current, total int64) {
		lastCurrent = current
		lastTotal = total

		if !isInteractiveMode {
			return
		}

		frame := styles.RenderMuted(spinnerFrames[spinnerIdx%len(spinnerFrames)])
		spinnerIdx++

		if total > 0 {
			fmt.Fprintf(os.Stderr, "\r %s %s... %s / %s",
				frame, label,
				formatMB(current), formatMB(total))
		} else {
			fmt.Fprintf(os.Stderr, "\r %s %s... %s",
				frame, label, formatMB(current))
		}
	}

	err := work(update)
	elapsed := time.Since(start)

	if isInteractiveMode {
		// Clear the progress line.
		fmt.Fprintf(os.Stderr, "\r\033[K")
	}

	if err != nil {
		log.Info().Msgf(" %s %s %s", styles.RenderError("✗"), label,
			styles.RenderError("[failed]"))
		return err
	}

	sizeStr := ""
	if lastTotal > 0 {
		sizeStr = fmt.Sprintf("(%s) ", formatMB(lastTotal))
	} else if lastCurrent > 0 {
		sizeStr = fmt.Sprintf("(%s) ", formatMB(lastCurrent))
	}

	log.Info().Msgf(" %s %s %s%s", styles.RenderSuccess("✓"), label, sizeStr,
		styles.RenderMuted(fmt.Sprintf("[%.1fs]", elapsed.Seconds())))

	return nil
}

// formatMB formats a byte count as megabytes, always using MB units.
func formatMB(b int64) string {
	mb := float64(b) / (1024 * 1024)
	return fmt.Sprintf("%.1f MB", mb)
}
