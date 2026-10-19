/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/metaplay/cli/pkg/portalapi"
)

func testOrgs() []portalapi.OrganizationWithProjects {
	return []portalapi.OrganizationWithProjects{
		{
			Name: "Zeta Studio",
			Role: "member",
			Projects: []portalapi.ProjectInfo{
				{HumanID: "tiny-brave-otter", Name: "Puzzle Proto", Type: portalapi.ProjectTierFree},
			},
		},
		{
			Name: "Acme Games",
			Role: "owner",
			Projects: []portalapi.ProjectInfo{
				{HumanID: "quick-red-fox", Name: "Space Miners", Type: portalapi.ProjectTierProduction},
				{HumanID: "fancy-gorgeous-bear", Name: "Card Battler", Type: portalapi.ProjectTierPreLaunch},
			},
		},
		{Name: "empty org", Role: "member"},
	}
}

// Organizations and their projects are listed by name ignoring case, whatever
// order the portal returns them in.
func TestSortOrgsAndProjects_SortsByName(t *testing.T) {
	orgs := testOrgs()
	sortOrgsAndProjects(orgs)

	var got []string
	for _, org := range orgs {
		got = append(got, org.Name)
		for _, project := range org.Projects {
			got = append(got, "  "+project.Name)
		}
	}
	want := []string{"Acme Games", "  Card Battler", "  Space Miners", "empty org", "Zeta Studio", "  Puzzle Proto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %q\nwant    %q", got, want)
	}
}

// The JSON output groups projects by organization, with the CLI's own field
// names rather than the portal's. An organization without projects is kept,
// with an empty list rather than null.
func TestOrgListItems_GroupsByOrgWithCLIFieldNames(t *testing.T) {
	orgs := testOrgs()
	sortOrgsAndProjects(orgs)

	out, err := json.Marshal(orgListItems(orgs[:2]))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"organization":"Acme Games","role":"owner","projects":[` +
		`{"projectId":"fancy-gorgeous-bear","name":"Card Battler","tier":"pre-launch"},` +
		`{"projectId":"quick-red-fox","name":"Space Miners","tier":"production"}]},` +
		`{"organization":"empty org","role":"member","projects":[]}]`
	if string(out) != want {
		t.Errorf("json = %s\nwant   %s", out, want)
	}
}

// A missing project found from the current directory only drops the marker,
// but a --project path that does not resolve is an error, as for other commands.
func TestFindCurrentProjectID_ErrorsOnlyForExplicitPath(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Cleanup(func() { flagProjectConfigPath = "" })

	flagProjectConfigPath = ""
	if id, err := findCurrentProjectID(); id != "" || err != nil {
		t.Errorf("without --project: id = %q, err = %v; want \"\", nil", id, err)
	}

	flagProjectConfigPath = filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := findCurrentProjectID(); err == nil {
		t.Error("with a bad --project path: got no error")
	}
}

// The project in metaplay-project.yaml is marked, and only that one.
func TestProjectsTable_MarksCurrentProject(t *testing.T) {
	projects := testOrgs()[1].Projects

	rows, marked := projectsTable(projects, "fancy-gorgeous-bear")
	want := [][]string{
		{"quick-red-fox", "Space Miners", "production"},
		{"fancy-gorgeous-bear *", "Card Battler", "pre-launch"},
	}
	if !reflect.DeepEqual(rows, want) || !marked {
		t.Errorf("rows = %q, marked = %v\nwant   %q, true", rows, marked, want)
	}

	if _, marked := projectsTable(projects, ""); marked {
		t.Error("marked a project with no current project")
	}
}

func TestProjectsSummary_Pluralizes(t *testing.T) {
	for want, counts := range map[string][2]int{
		"15 projects in 1 organization": {15, 1},
		"1 project in 2 organizations":  {1, 2},
		"0 projects in 1 organization":  {0, 1},
	} {
		if got := projectsSummary(counts[0], counts[1]); got != want {
			t.Errorf("projectsSummary(%d, %d) = %q, want %q", counts[0], counts[1], got, want)
		}
	}
}

// Columns line up under their headings, and lines carry no trailing spaces.
func TestRenderTextTable_AlignsColumns(t *testing.T) {
	columns := []textColumn{{"ID", nil}, {"NAME", nil}, {"LAST", nil}}
	lines := renderTextTable(columns, [][]string{
		{"long-identifier", "a", ""},
		{"x", "bbbbbb", "z"},
	})

	want := []string{
		"  ID               NAME    LAST",
		"  long-identifier  a",
		"  x                bbbbbb  z",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q\nwant    %q", lines, want)
	}
}

// Widths are measured on screen, not in bytes: 'ä' is one column wide and
// each CJK character two, so the next column still lines up.
func TestRenderTextTable_AlignsNonASCII(t *testing.T) {
	columns := []textColumn{{"NAME", nil}, {"TIER", nil}}
	lines := renderTextTable(columns, [][]string{
		{"Pelikäs", "free"},
		{"游戏", "custom"},
	})

	want := []string{
		"  NAME     TIER",
		"  Pelikäs  free",
		"  游戏     custom",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q\nwant    %q", lines, want)
	}
}
