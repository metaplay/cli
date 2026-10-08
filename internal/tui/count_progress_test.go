/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func plain(out *bytes.Buffer) string {
	return ansiCodes.ReplaceAllString(out.String(), "")
}

// Without a terminal nothing can be redrawn, so each phase is a line when it
// starts and a line when it ends, saying how many it counted and how long it
// took. Counts in between would be a line each, which is noise in a log.
func TestCountProgress_LogsEachPhaseStartingAndEndingWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	progress := NewCountProgress(&out, false)

	progress.Update("Listing tags", 0, 0)
	progress.Update("Listing tags", 1000, 0)
	progress.Update("Listing tags", 1500, 0)
	progress.Update("Resolving tags", 0, 1500)
	progress.Update("Resolving tags", 750, 1500)
	progress.Update("Resolving tags", 1500, 1500)
	progress.Finish(nil)

	lines := strings.Split(strings.TrimSpace(plain(&out)), "\n")
	want := []*regexp.Regexp{
		regexp.MustCompile(`^Listing tags\.\.\.$`),
		regexp.MustCompile(`^ ✓ Listing tags \(1500\) \[\d+\.\ds\]$`),
		regexp.MustCompile(`^Resolving tags\.\.\.$`),
		regexp.MustCompile(`^ ✓ Resolving tags \(1500\) \[\d+\.\ds\]$`),
	}
	if len(lines) != len(want) {
		t.Fatalf("output:\n%s\nwant %d lines", plain(&out), len(want))
	}
	for i := range want {
		if !want[i].MatchString(lines[i]) {
			t.Errorf("line %d = %q, want it to match %s", i, lines[i], want[i])
		}
	}
}

// On a terminal the phase in progress is one line, redrawn in place with its
// count, and replaced by the same summary line when it ends.
func TestCountProgress_RedrawsThePhaseInPlaceOnATerminal(t *testing.T) {
	var out bytes.Buffer
	progress := NewCountProgress(&out, true)

	progress.Update("Reading images", 0, 3)
	progress.Finish(nil)

	got := plain(&out)
	if !strings.Contains(got, "\r") || !strings.Contains(got, "Reading images... 0 / 3") {
		t.Errorf("output %q does not redraw the phase with its count", got)
	}
	if !regexp.MustCompile(` ✓ Reading images \(0\) \[\d+\.\ds\]\n$`).MatchString(got) {
		t.Errorf("output %q does not end with the phase's summary", got)
	}
}

// A phase the work failed in is marked failed rather than done.
func TestCountProgress_MarksThePhaseItFailedIn(t *testing.T) {
	var out bytes.Buffer
	progress := NewCountProgress(&out, false)

	progress.Update("Listing tags", 0, 0)
	progress.Finish(errors.New("unauthorized"))

	if got := plain(&out); !strings.HasSuffix(got, " ✗ Listing tags [failed]\n") {
		t.Errorf("output %q does not end marking the phase failed", got)
	}
}

// Work that never started a phase leaves nothing behind.
func TestCountProgress_SaysNothingForWorkThatStartedNoPhase(t *testing.T) {
	var out bytes.Buffer
	NewCountProgress(&out, true).Finish(nil)

	if out.Len() != 0 {
		t.Errorf("output = %q, want none", out.String())
	}
}

// Work can fail in one phase and carry on another way. The phase it failed in
// is marked failed rather than done, and the next phase starts afresh.
func TestCountProgress_MarksAPhaseFailedAndCarriesOn(t *testing.T) {
	var out bytes.Buffer
	progress := NewCountProgress(&out, false)

	progress.Update("Reading manifests", 3, 10)
	progress.Finish(errors.New("access denied"))
	progress.Update("Listing tags", 0, 0)
	progress.Finish(nil)

	lines := strings.Split(strings.TrimSpace(plain(&out)), "\n")
	want := []*regexp.Regexp{
		regexp.MustCompile(`^Reading manifests\.\.\.$`),
		regexp.MustCompile(`^ ✗ Reading manifests \[failed\]$`),
		regexp.MustCompile(`^Listing tags\.\.\.$`),
		regexp.MustCompile(`^ ✓ Listing tags \(0\) \[\d+\.\ds\]$`),
	}
	if len(lines) != len(want) {
		t.Fatalf("output:\n%s\nwant %d lines", plain(&out), len(want))
	}
	for i := range want {
		if !want[i].MatchString(lines[i]) {
			t.Errorf("line %d = %q, want it to match %s", i, lines[i], want[i])
		}
	}
}
