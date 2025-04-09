/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command for updating the 'environments' section in the 'metaplay-project.yaml'. The environments
// infos are fetched from the portal using the projectID (human ID) specified in the YAML file.
type updateProjectEnvironmentsOpts struct {
}

func init() {
	o := updateProjectEnvironmentsOpts{}

	cmd := &cobra.Command{
		Use:     "project-environments [flags]",
		Aliases: []string{"project-envs"},
		Short:   "Update the project's environments in the metaplay-project.yaml",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Update the environments in the metaplay-project.yaml from the Metaplay Portal.

			Related commands:
			- 'metaplay deploy server' ...
		`),
		Example: renderExample(`
			# Update the project environments from the portal.
			metaplay update project-environments
		`),
	}

	updateCmd.AddCommand(cmd)
}

func (o *updateProjectEnvironmentsOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *updateProjectEnvironmentsOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Always use Metaplay Auth for project initialization.
	authProvider, err := getAuthProvider(project, "metaplay")
	if err != nil {
		return err
	}

	// Ensure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	log.Info().Msgf("Update environments in metaplay-project.yaml..")

	// Fetch project information from the portal.
	portalClient := portalapi.NewClient(tokenSet)
	projectInfo, err := portalClient.FetchProjectInfo(project.Config.ProjectHumanID)
	if err != nil {
		return fmt.Errorf("failed to fetch project information from the portal: %w", err)
	}
	log.Debug().Msgf("Found project from portal: %+v", projectInfo)

	// Fetch project environments from the portal.
	// \todo This returns the environments that the user has privileges for -- this can lead
	//       some strange behavior in updating the projects as we don't have a way to distinguish
	//       between removed and unaccessible environment.
	projectEnvironments, err := portalClient.FetchProjectEnvironments(projectInfo.UUID)
	if err != nil {
		return fmt.Errorf("failed to fetch project environments from the portal: %w", err)
	}
	log.Debug().Msgf("Found following environments for project: %+v", projectEnvironments)

	// Update the environments in metaplay-project.yaml.
	err = o.updateProjectConfigEnvironments(project, projectEnvironments)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully updated environments!")
	return nil
}

// Update the metaplay-project.yaml to be up-to-date with newEnvironments.
// Use goccy/go-yaml for minimally editing the file, i.e., to retain ordering, comments,
// and whitespace in the untouched parts of the file.
func (o *updateProjectEnvironmentsOpts) updateProjectConfigEnvironments(project *metaproj.MetaplayProject, newPortalEnvironments []portalapi.EnvironmentInfo) error {
	// Load the existing YAML file
	projectConfigFilePath := filepath.Join(project.RelativeDir, metaproj.ConfigFileName)
	configFileBytes, err := os.ReadFile(projectConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed to read project config file: %v", err)
	}

	root, err := parser.ParseBytes(configFileBytes, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	for _, portalEnv := range newPortalEnvironments {
		// Find the environments array
		envsPath, err := yaml.PathString("$.environments")
		if err != nil {
			return fmt.Errorf("failed to create environments path: %v", err)
		}

		// Get the environments node
		envsNode, err := envsPath.FilterFile(root)
		if err != nil {
			return fmt.Errorf("failed to get environments: %v", err)
		}

		// Find the index of the environment with matching humanId
		seqNode, ok := envsNode.(*ast.SequenceNode)
		if !ok {
			return fmt.Errorf("environments is not a sequence")
		}

		foundIndex := -1
		for i, envNode := range seqNode.Values {
			mapNode, ok := envNode.(*ast.MappingNode)
			if !ok {
				continue
			}
			for _, value := range mapNode.Values {
				if value.Key.GetToken().Value == "humanId" && value.Value.GetToken().Value == portalEnv.HumanID {
					foundIndex = i
					break
				}
			}
			if foundIndex != -1 {
				break
			}
		}

		// Initialize new project environment config (with fresh information from portal).
		newEnvConfig := metaproj.ProjectEnvironmentConfig{
			Name:        portalEnv.Name,
			HumanID:     portalEnv.HumanID,
			StackDomain: portalEnv.StackDomain,
			Type:        portalEnv.Type,
		}

		// If updating an existing environment, copy the fields from the original entry
		// that are not owned/known by the portal.
		if foundIndex != -1 {
			oldConfig, err := project.Config.GetEnvironmentByHumanID(portalEnv.HumanID)
			if err != nil {
				return err
			}
			newEnvConfig.ServerValuesFile = oldConfig.ServerValuesFile
			newEnvConfig.BotClientValuesFile = oldConfig.BotClientValuesFile
		}

		// Convert environment info to YAML.
		envYAML, err := yaml.Marshal(newEnvConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal environment info to YAML: %w", err)
		}

		// Parse environment YAML to AST.
		envAST, err := parser.ParseBytes(envYAML, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("failed to parse environment info to AST: %w", err)
		}

		// Update an existing node or append a new node to the end.
		if foundIndex == -1 {
			log.Info().Msgf("Add new environment '%s'", portalEnv.HumanID)
			seqNode.Values = append(seqNode.Values, envAST.Docs[0].Body)
		} else {
			log.Info().Msgf("Update existing environment '%s'", portalEnv.HumanID)
			seqNode.Values[foundIndex] = envAST.Docs[0].Body
		}
	}

	// Write the updated YAML back to the file
	if err := os.WriteFile(projectConfigFilePath, []byte(root.String()), 0644); err != nil {
		return fmt.Errorf("failed to write updated config: %v", err)
	}

	return nil
}
