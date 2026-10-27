/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getProjectsOpts struct {
	flagFormat string
}

// orgListItem is one organization and its projects in the JSON output. The CLI
// owns this shape, so portal API changes do not change what scripts and agents read.
type orgListItem struct {
	Organization string            `json:"organization"`
	Role         string            `json:"role"`
	Projects     []projectListItem `json:"projects"`
}

type projectListItem struct {
	ProjectID string                `json:"projectId"`
	Name      string                `json:"name"`
	Tier      portalapi.ProjectTier `json:"tier"`
}

func init() {
	o := getProjectsOpts{}

	cmd := &cobra.Command{
		Use:   "projects [flags]",
		Short: "List the Metaplay portal projects you can access",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			List the projects you can access in the Metaplay portal, grouped by organization.

			The project ID is what 'projectID' in metaplay-project.yaml refers to. When run in a
			project directory, the text output marks that project with '*'.

			You must be logged in to the Metaplay portal (use 'metaplay auth login').

			Related commands:
			- 'metaplay update project-environments' writes the project's environments into metaplay-project.yaml.
		`),
		Example: renderExample(`
			# List your projects.
			metaplay get projects

			# List your projects in JSON format.
			metaplay get projects --format=json
		`),
	}

	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format: 'text' or 'json'")
}

func (o *getProjectsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return clierrors.NewUsageErrorf("Invalid format %q", o.flagFormat).
			WithSuggestion("Use 'text' or 'json'")
	}
	return nil
}

func (o *getProjectsOpts) Run(cmd *cobra.Command) error {
	// JSON is parsed from stdout, so the log (login prompts, --verbose output)
	// goes to stderr, and the JSON is written without the logger's decorations.
	if o.flagFormat == "json" {
		log.Logger = stderrLogger
	}

	currentProjectID, err := findCurrentProjectID()
	if err != nil {
		return err
	}

	// The portal always uses the default auth provider.
	authProvider, err := auth.NewDefaultAuthProvider()
	if err != nil {
		return err
	}
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	portalClient := portalapi.NewClient(tokenSet)
	orgs, err := portalClient.FetchUserOrgsAndProjects()
	if err != nil {
		return err
	}
	sortOrgsAndProjects(orgs)

	if o.flagFormat == "json" {
		projectsJSON, err := json.MarshalIndent(orgListItems(orgs), "", "  ")
		if err != nil {
			return clierrors.Wrap(err, "Failed to marshal projects as JSON")
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(projectsJSON)); err != nil {
			return clierrors.Wrap(err, "Failed to write projects JSON")
		}
		return nil
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Projects"))

	numProjects := 0
	markedCurrent := false
	for _, org := range orgs {
		log.Info().Msg("")
		log.Info().Msgf("%s  %s", styles.RenderBright(org.Name), styles.RenderMuted(fmt.Sprintf("(role: %s)", org.Role)))
		if len(org.Projects) == 0 {
			log.Info().Msg("  No accessible projects.")
			continue
		}
		rows, marked := projectsTable(org.Projects, currentProjectID)
		for _, line := range renderTextTable(projectListColumns, rows) {
			log.Info().Msg(line)
		}
		numProjects += len(org.Projects)
		markedCurrent = markedCurrent || marked
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderMuted("  " + projectsSummary(numProjects, len(orgs))))
	if numProjects == 0 {
		log.Info().Msg(styles.RenderMuted("  Create a project at https://portal.metaplay.dev, or ask your team for access to one."))
	}
	if markedCurrent {
		log.Info().Msg(styles.RenderMuted("  * Current project (from metaplay-project.yaml)"))
	}
	log.Info().Msg("")

	return nil
}

// findCurrentProjectID returns the project ID in the metaplay-project.yaml
// found from the current directory, or "" if there is none. The ID only marks
// a row, so a missing or broken file found that way is not an error. A path
// given with --project is, as it is for other commands.
func findCurrentProjectID() (string, error) {
	projectDir, err := findProjectDirectory()
	if errors.Is(err, errProjectNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	config, err := metaproj.LoadProjectConfigFile(projectDir)
	if err != nil {
		if flagProjectConfigPath != "" {
			return "", err
		}
		log.Debug().Msgf("Ignoring unreadable project config: %v", err)
		return "", nil
	}
	return config.ProjectHumanID, nil
}

// sortOrgsAndProjects sorts organizations, and the projects in each, by name
// ignoring case, so the output does not depend on the order the portal returns.
func sortOrgsAndProjects(orgs []portalapi.OrganizationWithProjects) {
	byName := func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a), strings.ToLower(b)), cmp.Compare(a, b))
	}
	slices.SortStableFunc(orgs, func(a, b portalapi.OrganizationWithProjects) int {
		return byName(a.Name, b.Name)
	})
	for _, org := range orgs {
		slices.SortStableFunc(org.Projects, func(a, b portalapi.ProjectInfo) int {
			return cmp.Or(byName(a.Name, b.Name), cmp.Compare(a.HumanID, b.HumanID))
		})
	}
}

// orgListItems converts the portal's organizations to the JSON output. Lists
// are always non-nil, so an organization without projects marshals as [].
func orgListItems(orgs []portalapi.OrganizationWithProjects) []orgListItem {
	items := []orgListItem{}
	for _, org := range orgs {
		projects := []projectListItem{}
		for _, project := range org.Projects {
			projects = append(projects, projectListItem{
				ProjectID: project.HumanID,
				Name:      project.Name,
				Tier:      project.Type,
			})
		}
		items = append(items, orgListItem{Organization: org.Name, Role: org.Role, Projects: projects})
	}
	return items
}

// projectsSummary says how many projects and organizations were listed, eg,
// "15 projects in 1 organization".
func projectsSummary(numProjects, numOrgs int) string {
	plural := func(n int, noun string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, noun)
		}
		return fmt.Sprintf("%d %ss", n, noun)
	}
	return plural(numProjects, "project") + " in " + plural(numOrgs, "organization")
}

var projectListColumns = []textColumn{
	{"PROJECT ID", styles.RenderTechnical},
	{"NAME", nil},
	{"TIER", nil},
}

// projectsTable returns a row per project, and whether one of them was marked
// as the current project.
func projectsTable(projects []portalapi.ProjectInfo, currentProjectID string) (rows [][]string, marked bool) {
	for _, project := range projects {
		id := project.HumanID
		if currentProjectID != "" && id == currentProjectID {
			id += " *"
			marked = true
		}
		rows = append(rows, []string{id, project.Name, string(project.Type)})
	}
	return rows, marked
}
