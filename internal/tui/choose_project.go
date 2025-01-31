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

func chooseOrganization(organizations []portalapi.PortalOrganizationInfo, orgToProjects map[string][]portalapi.PortalProjectInfo) (*portalapi.PortalOrganizationInfo, error) {
	// Create list items
	items := make([]list.Item, len(organizations))
	for ndx, org := range organizations {
		projectCount := len(orgToProjects[org.UUID])
		items[ndx] = compactListItem{
			index:       ndx,
			name:        org.Name,
			description: fmt.Sprintf("(%s)", pluralize(projectCount, "project")),
		}
	}

	// Let the user choose the organization
	chosen, err := chooseFromList("Select target organization:", items)
	if err != nil {
		return nil, err
	}

	return &organizations[chosen], nil
}

func chooseProject(projects []portalapi.PortalProjectInfo) (*portalapi.PortalProjectInfo, error) {
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
	chosen, err := chooseFromList("Select target project:", items)
	if err != nil {
		return nil, err
	}

	return &projects[chosen], nil
}

// ChooseOrgAndProject fetches all the organizations and projects from the portal (that the user has
// access to) and then displays an interactive list for the user to choose the project from.
func ChooseOrgAndProject(tokenSet *auth.TokenSet) (*portalapi.PortalProjectInfo, error) {
	if !isInteractiveMode {
		return nil, fmt.Errorf("interactive mode required for project selection")
	}

	// \todo replace the fetching with Teemu's upcoming API

	// Get available organizations from the portal.
	portalClient := portalapi.NewClient(tokenSet)
	organizations, err := portalClient.FetchAllOrganizations()
	if err != nil {
		return nil, err
	}
	if len(organizations) == 0 {
		return nil, fmt.Errorf("no accessible organizations found in the portal")
	}

	// Get available projects from the portal.
	projects, err := portalClient.FetchAllProjects()
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no accessible projects found in the portal")
	}

	// Pre-compute organization to projects mapping
	orgToProjects := make(map[string][]portalapi.PortalProjectInfo)
	for _, proj := range projects {
		orgToProjects[proj.OrganizationUUID] = append(orgToProjects[proj.OrganizationUUID], proj)
	}

	// Let the user choose their organization.
	chosenOrg, err := chooseOrganization(organizations, orgToProjects)
	if err != nil {
		return nil, err
	}

	orgProjects := orgToProjects[chosenOrg.UUID]
	if len(orgProjects) == 0 {
		return nil, fmt.Errorf("no projects found in the chosen organization")
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), chosenOrg.Name)

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
