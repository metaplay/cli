/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/metaplay/cli/pkg/styles"
)

// countProgressRedraw is how often a terminal's progress line is redrawn, so
// the spinner moves even while a count does not.
const countProgressRedraw = 100 * time.Millisecond

// CountProgress shows how far work made of phases has got, each phase counting
// things done. It writes to its own writer rather than the log, so a command
// whose output is data can show progress on stderr and keep stdout clean.
//
// On a terminal the phase in progress is one line redrawn in place, with a
// spinner and its count. Without one, each phase is a line when it starts and
// another when it ends. Either way a finished phase leaves a summary line.
type CountProgress struct {
	out         io.Writer
	interactive bool

	mu           sync.Mutex
	phase        string
	done         int
	total        int
	phaseStarted time.Time
	frame        int
	stopTicking  chan struct{}
}

// NewCountProgress shows progress on out, redrawing in place where interactive.
func NewCountProgress(out io.Writer, interactive bool) *CountProgress {
	return &CountProgress{out: out, interactive: interactive}
}

// Update reports how far the work has got: done of total in phase, where a
// total of zero is not known. A new phase ends the one before it.
func (p *CountProgress) Update(phase string, done, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if phase != p.phase {
		if p.phase != "" {
			p.endPhase(nil)
		}
		p.phase = phase
		p.phaseStarted = time.Now()
		p.done, p.total = done, total
		if p.interactive {
			p.draw()
			p.startTicking()
		} else {
			_, _ = fmt.Fprintf(p.out, "%s...\n", phase)
		}
		return
	}
	p.done, p.total = done, total
}

// Finish ends the phase in progress, as failed where err is not nil. Work that
// carries on another way can go on to start more phases.
func (p *CountProgress) Finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase != "" {
		p.endPhase(err)
		p.phase = ""
	}
}

// endPhase replaces the phase's progress with its summary line. Called with
// the lock held.
func (p *CountProgress) endPhase(err error) {
	if p.interactive {
		p.stopTickingLocked()
		_, _ = fmt.Fprint(p.out, "\r\033[K")
	}
	if err != nil {
		_, _ = fmt.Fprintf(p.out, " %s %s %s\n", styles.RenderError("✗"), p.phase, styles.RenderError("[failed]"))
		return
	}
	elapsed := time.Since(p.phaseStarted)
	_, _ = fmt.Fprintf(p.out, " %s %s (%d) %s\n", styles.RenderSuccess("✓"), p.phase, p.done,
		styles.RenderMuted(fmt.Sprintf("[%.1fs]", elapsed.Seconds())))
}

// draw redraws the phase's line in place. Called with the lock held.
func (p *CountProgress) draw() {
	frame := styles.RenderMuted(spinnerFrames[p.frame%len(spinnerFrames)])
	p.frame++
	if p.total > 0 {
		_, _ = fmt.Fprintf(p.out, "\r %s %s... %d / %d", frame, p.phase, p.done, p.total)
	} else {
		_, _ = fmt.Fprintf(p.out, "\r %s %s... %d", frame, p.phase, p.done)
	}
}

// startTicking redraws the line on a timer until the phase ends. Called with
// the lock held.
func (p *CountProgress) startTicking() {
	stop := make(chan struct{})
	p.stopTicking = stop
	go func() {
		ticker := time.NewTicker(countProgressRedraw)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				p.mu.Lock()
				// A phase ending closes stop with the lock held, so a tick
				// that waited for the lock checks again before drawing.
				select {
				case <-stop:
				default:
					p.draw()
				}
				p.mu.Unlock()
			}
		}
	}()
}

// stopTickingLocked stops the redraw timer. The ticker goroutine may be
// waiting for the lock the caller holds, so this does not wait for it to exit;
// it sees stop closed once it gets the lock, draws nothing and returns.
func (p *CountProgress) stopTickingLocked() {
	if p.stopTicking != nil {
		close(p.stopTicking)
		p.stopTicking = nil
	}
}
