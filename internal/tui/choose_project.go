/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"fmt"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

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
	selectedOrg, err := ChooseFromListDialog(
		"Choose Target Organization",
		orgsAndProjects,
		func(org *portalapi.OrganizationWithProjects) (string, string) {
			return org.Name, fmt.Sprintf("(%s)", pluralize(len(org.Projects), "project"))
		})
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
	selectedProject, err := ChooseProjectDialog(orgProjects)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), selectedProject.Name, styles.RenderMuted(fmt.Sprintf("[%s]", selectedProject.HumanID)))

	return selectedProject, nil
}

func ChooseProjectDialog(projects []portalapi.ProjectInfo) (*portalapi.ProjectInfo, error) {
	return ChooseFromListDialog[portalapi.ProjectInfo](
		"Select Target Project",
		projects,
		func(proj *portalapi.ProjectInfo) (string, string) {
			return proj.Name, fmt.Sprintf("[%s]", proj.HumanID)
		},
	)
}

func ChooseEnvironmentDialog(environments []portalapi.EnvironmentInfo) (*portalapi.EnvironmentInfo, error) {
	return ChooseFromListDialog[portalapi.EnvironmentInfo](
		"Select Target Environment",
		environments,
		func(env *portalapi.EnvironmentInfo) (string, string) {
			return env.Name, fmt.Sprintf("[%s]", env.HumanID)
		},
	)
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
