/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"log/slog"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
)

// Install and upgrade must use client-side apply, as Helm v3 did. Helm rejects
// ForceConflicts under client-side apply, and ForceReplace drops labels and
// finalizers that others added to the objects.
func TestHelmActionSettings(t *testing.T) {
	cfg := &action.Configuration{}

	install := newInstallAction(cfg, "ns", "rel", "1.2.3", time.Minute, true)
	if install.ServerSideApply {
		t.Error("install: ServerSideApply = true, want client-side apply")
	}
	if install.ForceConflicts {
		t.Error("install: ForceConflicts = true, want false")
	}
	if install.WaitStrategy != kube.StatusWatcherStrategy {
		t.Errorf("install: WaitStrategy = %q, want %q", install.WaitStrategy, kube.StatusWatcherStrategy)
	}

	upgrade := newUpgradeAction(cfg, "ns", "1.2.3", time.Minute, true)
	if upgrade.ServerSideApply != "false" {
		t.Errorf("upgrade: ServerSideApply = %q, want \"false\"", upgrade.ServerSideApply)
	}
	if upgrade.ForceConflicts {
		t.Error("upgrade: ForceConflicts = true, want false")
	}
	if upgrade.ForceReplace {
		t.Error("upgrade: ForceReplace = true, want false")
	}
	if upgrade.WaitStrategy != kube.StatusWatcherStrategy {
		t.Errorf("upgrade: WaitStrategy = %q, want %q", upgrade.WaitStrategy, kube.StatusWatcherStrategy)
	}
}

func TestLineHandler(t *testing.T) {
	var lines []string
	logger := slog.New(newLineHandler(func(line string) { lines = append(lines, line) }, slog.LevelInfo))

	logger.Debug("dropped")
	logger.Info("preparing upgrade", "name", "rel")
	logger.Warn("something odd")

	want := []string{
		`msg="preparing upgrade" name=rel`,
		`level=WARN msg="something odd"`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %q", len(lines), lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
