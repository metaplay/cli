/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/metaproj"
)

func TestRenderPnpmWorkspaceContent_WithAllowBuilds(t *testing.T) {
	got, err := renderPnpmWorkspaceContent(
		[]string{"MetaplaySDK/Frontend/*", "Backend/Dashboard"},
		[]string{"@parcel/watcher", "bootstrap-vue", "esbuild"},
	)
	if err != nil {
		t.Fatalf("renderPnpmWorkspaceContent returned error: %v", err)
	}

	out := string(got)

	wantContains := []string{
		"packages:",
		"- MetaplaySDK/Frontend/*",
		"- Backend/Dashboard",
		"allowBuilds:",
		"\"@parcel/watcher\": true",
		"bootstrap-vue: true",
		"esbuild: true",
	}
	for _, w := range wantContains {
		if !strings.Contains(out, w) {
			t.Errorf("expected output to contain %q, got:\n%s", w, out)
		}
	}
}

func TestRenderPnpmWorkspaceContent_WithoutAllowBuilds(t *testing.T) {
	for name, allowBuilds := range map[string][]string{
		"nil slice":   nil,
		"empty slice": {},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := renderPnpmWorkspaceContent(
				[]string{"MetaplaySDK/Frontend/*", "Backend/Dashboard"},
				allowBuilds,
			)
			if err != nil {
				t.Fatalf("renderPnpmWorkspaceContent returned error: %v", err)
			}
			out := string(got)
			if !strings.Contains(out, "packages:") {
				t.Errorf("expected 'packages:' in output, got:\n%s", out)
			}
			if strings.Contains(out, "allowBuilds") {
				t.Errorf("did not expect 'allowBuilds' in output, got:\n%s", out)
			}
		})
	}
}

func TestComputeProjectConfigDashboardUpdate(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:   "dashboard section is updated",
			config: "projectID: test\nfeatures:\n  dashboard:\n    useCustom: false\n    rootDir:\n",
		},
		{
			name:    "missing dashboard section returns an error",
			config:  "projectID: test\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, metaproj.ConfigFileName), []byte(tc.config), 0644); err != nil {
				t.Fatal(err)
			}

			_, content, err := computeProjectConfigDashboardUpdate(&metaproj.MetaplayProject{RelativeDir: dir}, "Backend/Dashboard")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got content:\n%s", content)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(string(content), "useCustom: true") || !strings.Contains(string(content), "rootDir: Backend/Dashboard") {
				t.Errorf("dashboard section not updated:\n%s", content)
			}
		})
	}
}
