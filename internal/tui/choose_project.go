/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

func chooseOrganization(organizations []portalapi.OrganizationWithProjects) (*portalapi.OrganizationWithProjects, error) {
	// Create list items
	items := make([]list.Item, len(organizations))
	for ndx, org := range organizations {
		projectCount := len(org.Projects)
		items[ndx] = compactListItem{
			index:       ndx,
			name:        org.Name,
			description: fmt.Sprintf("(%s)", pluralize(projectCount, "project")),
		}
	}

	// Let the user choose the organization
	chosen, err := chooseFromList("Select Target Organization", items)
	if err != nil {
		return nil, err
	}

	return &organizations[chosen], nil
}

func chooseProject(projects []portalapi.ProjectInfo) (*portalapi.ProjectInfo, error) {
	// Create list items
	items := make([]list.Item, len(projects))
	for ndx, proj := range projects {
		items[ndx] = compactListItem{
			index:       ndx,
			name:        proj.Name,
			description: fmt.Sprintf("[%s]", proj.HumanID),
		}
	}

	// Let the user choose the project
	chosen, err := chooseFromList("Select Target Project", items)
	if err != nil {
		return nil, err
	}

	return &projects[chosen], nil
}

// ChooseOrgAndProject fetches all the organizations and projects from the portal (that the user has
// access to) and then displays an interactive list for the user to choose the project from.
func ChooseOrgAndProject(tokenSet *auth.TokenSet) (*portalapi.ProjectInfo, error) {
	if !isInteractiveMode {
		return nil, fmt.Errorf("interactive mode required for project selection")
	}

	// Get available organizations from the portal.
	portalClient := portalapi.NewClient(tokenSet)
	orgsAndProjects, err := portalClient.FetchUserOrgsAndProjects()
	if err != nil {
		return nil, err
	}
	if len(orgsAndProjects) == 0 {
		return nil, fmt.Errorf("no accessible organizations found in the portal")
	}

	// Let the user choose their organization.
	selectedOrg, err := chooseOrganization(orgsAndProjects)
	if err != nil {
		return nil, err
	}

	// Must have at least one project in the organization.
	orgProjects := selectedOrg.Projects
	if len(orgProjects) == 0 {
		return nil, fmt.Errorf("no projects found in the chosen organization")
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedOrg.Name)

	// Let the user choose the project (within the organization)
	selectedProject, err := chooseProject(orgProjects)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), selectedProject.Name, styles.RenderMuted(fmt.Sprintf("[%s]", selectedProject.HumanID)))

	return selectedProject, nil
}

// Pluralize a word based on the count. This is a dumb version that only adds an
// 's' suffix to the word so only works for simple cases.
func pluralize(count int, unit string) string {
	if count == 0 {
		return fmt.Sprintf("no %ss", unit)
	} else if count == 1 {
		return fmt.Sprintf("%d %s", count, unit)
	} else {
		return fmt.Sprintf("%d %ss", count, unit)
	}
}
