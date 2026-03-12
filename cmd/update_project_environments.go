/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
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
		Aliases: []string{"project-envs", "environments"},
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

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Update Environments from Portal"))
	log.Info().Msg("")

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

	// Fetch project environment client configs from the portal.
	// \todo why make two requests? can we combine this with the previous fetch?
	projectEnvClientConfigs, err := portalClient.FetchProjectEnvironmentClientConfigs(projectInfo.UUID)
	if err != nil {
		return clierrors.Wrap(err, "Unable to fetch environment configs from the portal")
	}
	log.Debug().Msgf("Found following environment client configs for project: %d environments", len(projectEnvClientConfigs))
	for _, config := range projectEnvClientConfigs {
		if config.ClientConfig != nil {
			log.Debug().Msgf("  %s %s: %+v", styles.RenderSuccess("✓"), styles.RenderTechnical(config.EnvironmentHumanID), config.ClientConfig)
		} else {
			log.Debug().Msgf("  %s %s: %s", styles.RenderError("✗"), styles.RenderTechnical(config.EnvironmentHumanID), *config.Error)
		}
	}

	// Update the environments in metaplay-project.yaml.
	err = o.updateProjectConfigEnvironments(project, projectEnvironments)
	if err != nil {
		return err
	}

	// Update the environment configs JSON file.
	err = o.updateEnvironmentConfigs(project, projectEnvClientConfigs)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully updated environments!"))
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

	// Find the 'environments' node -- should be an array but can also be null
	envsPath, err := yaml.PathString("$.environments")
	if err != nil {
		return fmt.Errorf("failed to create environments path: %v", err)
	}

	// Get the environments node
	envsNode, err := envsPath.FilterFile(root)
	if err != nil {
		return fmt.Errorf("failed to find 'environments' in metaplay-project.yaml: %v", err)
	}

	// Handle the case where environments exists but is null/empty (e.g., "environments:" with no value).
	// This happens with projects that have no environments in them.
	// Only convert null to a sequence if there are environments to add - otherwise keep as null.
	if _, isNull := envsNode.(*ast.NullNode); isNull {
		if len(newPortalEnvironments) == 0 {
			// No environments to add, keep the null node as-is to avoid outputting "environments: []"
			log.Info().Msgf("%s No environments found for this project in the portal.", styles.RenderMuted("i"))
			log.Info().Msg("")
			log.Info().Msgf("%s Updated environments in %s", styles.RenderSuccess("✓"), styles.RenderTechnical("metaplay-project.yaml"))
			return nil
		}

		// Replace the null node with an empty sequence
		if err := envsPath.ReplaceWithReader(root, strings.NewReader("[]")); err != nil {
			return fmt.Errorf("failed to replace null 'environments' with empty sequence: %v", err)
		}
		// Re-fetch the environments node after replacement
		envsNode, err = envsPath.FilterFile(root)
		if err != nil {
			return fmt.Errorf("failed to find node 'environments' after replacement: %v", err)
		}
		// Ensure block-style output (not flow-style like [a, b, c]) and reset indentation
		if seqNode, ok := envsNode.(*ast.SequenceNode); ok {
			seqNode.IsFlowStyle = false
			// Reset the Start token position to fix indentation (2 spaces indent)
			if seqNode.Start != nil {
				seqNode.Start.Position.Column = 3
				seqNode.Start.Position.IndentNum = 2
			}
		}
	}

	// Cast to sequence node (now guaranteed to be valid after null handling above)
	envsSeqNode, ok := envsNode.(*ast.SequenceNode)
	if !ok {
		return fmt.Errorf("the 'environments' node in metaplay-project.yaml is not a valid sequence")
	}

	// Ensure block-style output (not flow-style like []) when there are environments to add.
	// This handles the case where the YAML file already has "environments: []".
	// Also fix indentation when converting from flow-style to block-style.
	if len(newPortalEnvironments) > 0 {
		envsSeqNode.IsFlowStyle = false
		// Reset the Start token position to fix indentation (2 spaces indent)
		if envsSeqNode.Start != nil {
			envsSeqNode.Start.Position.Column = 3
			envsSeqNode.Start.Position.IndentNum = 2
		}
	}

	// Print a note if no environments exist in the portal.
	if len(newPortalEnvironments) == 0 {
		log.Info().Msgf("%s No environments found for this project in the portal.", styles.RenderMuted("i"))
	}

	// Handle all environments from the portal.
	for _, portalEnv := range newPortalEnvironments {
		// Find the index of the environment with matching humanId
		foundIndex := -1
		for i, envNode := range envsSeqNode.Values {
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
			HostingType: portalEnv.HostingType,
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
			log.Info().Msgf("%s Add new environment %s", styles.RenderSuccess("+"), styles.RenderTechnical(portalEnv.HumanID))
			envsSeqNode.Values = append(envsSeqNode.Values, envAST.Docs[0].Body)
		} else {
			log.Info().Msgf("%s Update existing environment %s", styles.RenderSuccess("*"), styles.RenderTechnical(portalEnv.HumanID))
			envsSeqNode.Values[foundIndex] = envAST.Docs[0].Body
		}
	}

	// Find any deleted environments. Only show a message if there are any.
	for _, envConfig := range project.Config.Environments {
		found := false
		for _, newEnv := range newPortalEnvironments {
			if newEnv.HumanID == envConfig.HumanID {
				found = true
				break
			}
		}
		if !found {
			log.Info().Msgf("%s Environment %s does not exist in portal; remove manually from metaplay-project.yaml if not needed", styles.RenderError("-"), styles.RenderTechnical(envConfig.HumanID))
		}
	}

	// Write the updated YAML back to the file
	if err := os.WriteFile(projectConfigFilePath, []byte(root.String()), 0644); err != nil {
		return fmt.Errorf("failed to write updated config: %v", err)
	}

	log.Info().Msg("")
	log.Info().Msgf("%s Updated environments in %s", styles.RenderSuccess("✓"), styles.RenderTechnical("metaplay-project.yaml"))

	return nil
}

// updateEnvironmentConfigs finds and updates the EnvironmentConfigs.json file with new client configs
// fetched from the portal. Uses untyped JSON to preserve any userland extensions in the config entries.
func (o *updateProjectEnvironmentsOpts) updateEnvironmentConfigs(project *metaproj.MetaplayProject, clientConfigs []portalapi.EnvironmentClientConfigResponse) error {
	// Try to find EnvironmentConfigs.json in the expected Unity location
	environmentConfigsPath := filepath.Join(project.GetUnityProjectDir(), "ProjectSettings", "Metaplay", "EnvironmentConfigs.json")

	// Check if the file exists
	if _, err := os.Stat(environmentConfigsPath); os.IsNotExist(err) {
		log.Warn().Msgf("EnvironmentConfigs.json not found at %s", environmentConfigsPath)
		log.Warn().Msg("Skipping environment configs update. To enable this feature, ensure the file exists in your Unity project.")
		return nil
	} else if err != nil {
		return clierrors.Wrap(err, "Unable to access EnvironmentConfigs.json")
	}

	log.Info().Msg("")
	log.Info().Msgf("Updating %s", styles.RenderTechnical("EnvironmentConfigs.json"))

	// Read and parse existing EnvironmentConfigs.json as untyped JSON to preserve userland data.
	existingBytes, err := os.ReadFile(environmentConfigsPath)
	if err != nil {
		return clierrors.Wrap(err, "Unable to read EnvironmentConfigs.json")
	}

	var existingConfigs []map[string]any
	if err := json.Unmarshal(existingBytes, &existingConfigs); err != nil {
		return clierrors.Wrap(err, "Unable to parse EnvironmentConfigs.json")
	}

	// Process each portal client config response.
	for _, response := range clientConfigs {
		if response.ClientConfig == nil {
			log.Warn().Msgf("  %s %s: %s", styles.RenderError("✗"), styles.RenderTechnical(response.EnvironmentHumanID), *response.Error)
			continue
		}

		portalConfig := response.ClientConfig

		// Transform the portal format into the EnvironmentConfigs.json entry format.
		newEntry := transformPortalConfigToEnvironmentConfig(portalConfig)

		// Find existing entry by matching on Id (humanId) for V2 entries,
		// or by ServerHost in ConnectionEndpointConfig for V1 migration.
		foundIndex := findExistingConfigIndex(existingConfigs, portalConfig.HumanId, portalConfig.ServerHost)

		if foundIndex == -1 {
			// Add new entry
			log.Info().Msgf("  %s Add %s", styles.RenderSuccess("+"), styles.RenderTechnical(portalConfig.HumanId))
			existingConfigs = append(existingConfigs, newEntry)
		} else {
			// Merge into existing entry, preserving userland fields
			log.Info().Msgf("  %s Update %s", styles.RenderSuccess("*"), styles.RenderTechnical(portalConfig.HumanId))
			existingConfigs[foundIndex] = mergeEnvironmentConfig(existingConfigs[foundIndex], newEntry)
		}
	}

	// Write updated configs back to disk with consistent formatting (indented, LF line endings).
	outputBytes, err := json.MarshalIndent(existingConfigs, "", "  ")
	if err != nil {
		return clierrors.Wrap(err, "Unable to serialize updated environment configs")
	}
	// Normalize line endings to LF and ensure trailing newline (matching C# serialization).
	output := strings.ReplaceAll(string(outputBytes), "\r\n", "\n") + "\n"

	if err := os.WriteFile(environmentConfigsPath, []byte(output), 0644); err != nil {
		return clierrors.Wrap(err, "Unable to write EnvironmentConfigs.json")
	}

	log.Info().Msgf("  %s Updated %s", styles.RenderSuccess("✓"), styles.RenderTechnical("EnvironmentConfigs.json"))
	return nil
}

// transformPortalConfigToEnvironmentConfig converts the portal's EnvironmentClientConfig format
// into the EnvironmentConfigs.json entry format used by the Unity SDK.
func transformPortalConfigToEnvironmentConfig(pc *portalapi.EnvironmentClientConfig) map[string]any {
	// Determine server port (first entry or 0)
	serverPort := 0
	if len(pc.ServerPorts) > 0 {
		serverPort = pc.ServerPorts[0]
	}
	serverPortForWebSocket := 0
	if len(pc.ServerPortsForWebSocket) > 0 {
		serverPortForWebSocket = pc.ServerPortsForWebSocket[0]
	}

	// Build backup gateways from additional ports
	backupGateways := []any{}
	if len(pc.ServerPorts) > 1 {
		for _, port := range pc.ServerPorts[1:] {
			backupGateways = append(backupGateways, map[string]any{
				"Id":         "",
				"ServerHost": pc.ServerHost,
				"ServerPort": port,
			})
		}
	}

	// Determine auth token provider based on environment family
	authTokenProvider := "None"
	if pc.EnvironmentFamily != "Local" {
		authTokenProvider = "OAuth2"
	}

	// Build OAuth2 local callback URLs
	localCallbackUrls := []any{}
	if pc.OAuth2LocalCallback != "" {
		localCallbackUrls = append(localCallbackUrls, pc.OAuth2LocalCallback)
	}

	return map[string]any{
		"Version":     2,
		"Id":          pc.HumanId,
		"DisplayName": pc.DisplayName,
		"Description": "",
		"ConnectionEndpointConfig": map[string]any{
			"ServerHost":             pc.ServerHost,
			"ServerPort":             serverPort,
			"ServerPortForWebSocket": serverPortForWebSocket,
			"EnableTls":              pc.EnableTls,
			"CdnBaseUrl":             pc.CdnBaseUrl,
			"PublicWebApiUrl":         pc.PublicWebApiUrl,
			"BackupGateways":         backupGateways,
			"IsOfflineMode":          false,
		},
		"ClientLoggingConfig": map[string]any{
			"LogLevel":          "Debug",
			"LogLevelOverrides": []any{},
		},
		"ClientGameConfigBuildApiConfig": map[string]any{
			"AdminApiBaseUrl":                     pc.AdminApiBaseUrl,
			"AdminApiAuthenticationTokenProvider": authTokenProvider,
			"AdminApiCredentialsPath":             "",
			"AdminApiOAuth2Settings": map[string]any{
				"ClientId":              pc.OAuth2ClientID,
				"ClientSecret":          pc.OAuth2ClientSecret,
				"AuthorizationEndpoint": pc.OAuth2AuthorizationEndpoint,
				"TokenEndpoint":         pc.OAuth2TokenEndpoint,
				"Audience":              pc.OAuth2Audience,
				"LocalCallbackUrls":     localCallbackUrls,
				"UseStateParameter":     pc.OAuth2UseStateParameter,
			},
		},
	}
}

// findExistingConfigIndex finds the index of an existing environment config entry
// by matching on Id (humanId) for V2 entries, or by ServerHost for V1 migration.
func findExistingConfigIndex(configs []map[string]any, humanId string, serverHost string) int {
	// First pass: match by Id (V2 entries)
	for i, config := range configs {
		if id, ok := config["Id"].(string); ok && id == humanId {
			return i
		}
	}

	// Second pass: match by ServerHost in ConnectionEndpointConfig (V1 migration)
	for i, config := range configs {
		connConfig, ok := config["ConnectionEndpointConfig"].(map[string]any)
		if !ok {
			continue
		}
		if host, ok := connConfig["ServerHost"].(string); ok && host == serverHost {
			return i
		}
	}

	return -1
}

// mergeEnvironmentConfig merges a new config entry into an existing one.
// Portal-managed fields are overwritten; any extra userland fields in the existing
// entry are preserved.
func mergeEnvironmentConfig(existing, incoming map[string]any) map[string]any {
	result := make(map[string]any)

	// Copy all existing fields first (preserves userland extensions)
	for k, v := range existing {
		result[k] = v
	}

	// Overwrite with incoming portal-managed fields
	for k, v := range incoming {
		if k == "ConnectionEndpointConfig" || k == "ClientGameConfigBuildApiConfig" {
			// Deep merge nested objects to preserve userland sub-fields
			existingSub, existOk := existing[k].(map[string]any)
			incomingSub, incomOk := v.(map[string]any)
			if existOk && incomOk {
				merged := make(map[string]any)
				for sk, sv := range existingSub {
					merged[sk] = sv
				}
				for sk, sv := range incomingSub {
					// For AdminApiOAuth2Settings, also deep merge
					if sk == "AdminApiOAuth2Settings" {
						existOAuth, eOk := existingSub[sk].(map[string]any)
						incOAuth, iOk := sv.(map[string]any)
						if eOk && iOk {
							oauthMerged := make(map[string]any)
							for ok2, ov := range existOAuth {
								oauthMerged[ok2] = ov
							}
							for ok2, ov := range incOAuth {
								oauthMerged[ok2] = ov
							}
							merged[sk] = oauthMerged
							continue
						}
					}
					merged[sk] = sv
				}
				result[k] = merged
				continue
			}
		}
		result[k] = v
	}

	return result
}
