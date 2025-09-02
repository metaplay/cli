/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getEnvironmentInfoOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
}

func init() {
	o := getEnvironmentInfoOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:     "environment-info ENVIRONMENT [flags]",
		Aliases: []string{"env-info"},
		Short:   "Get information about the target environment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Get information about the target environment.

			By default, displays the most relevant information in a human-readable text format.
			Use --format=json to get the complete environment information in JSON format.

			{Arguments}

			Related commands:
			- 'metaplay get server-info ...' to get information about the game server deployment.
			- 'metaplay debug server-status ...' to get diagnostics about the health of the deployment.
		`),
		Example: renderExample(`
			# Show relevant environment information in text format (default)
			metaplay get environment-info tough-falcons

			# Show complete environment information in JSON format
			metaplay get environment-info tough-falcons --format=json
		`),
	}

	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'")
}

func (o *getEnvironmentInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	return nil
}

func intListToStr(ints []int) string {
	// Convert each integer to string
	strInts := make([]string, len(ints))
	for i, v := range ints {
		strInts[i] = strconv.Itoa(v)
	}
	// Join with commas
	return "[ " + strings.Join(strInts, ", ") + " ]"
}

func (o *getEnvironmentInfoOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Fetch the information from the environment via StackAPI.
	envInfo, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Get Kubernetes client to fetch database information
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get Kubernetes client for database info")
		kubeCli = nil // Continue without database info
	}

	// Fetch database shard configuration if Kubernetes client is available
	var shards []kubeutil.DatabaseShardConfig
	if kubeCli != nil {
		log.Debug().Str("namespace", kubeCli.Namespace).Msg("Fetching database shard configuration")
		shards, err = kubeutil.FetchDatabaseShardsFromSecret(cmd.Context(), kubeCli, kubeCli.Namespace)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to fetch database shard configuration")
			shards = nil // Continue without database info
		} else {
			// Fill in shard indices
			for shardNdx := range shards {
				shards[shardNdx].ShardIndex = shardNdx
			}
		}
	}

	// Only fetch portal info if targeting a managed stack.
	var portalInfo *portalapi.EnvironmentInfo
	authProviderName := coalesceString(envConfig.AuthProvider, "metaplay")
	if authProviderName == "metaplay" {
		// Fetch information from the portal.
		portalClient := portalapi.NewClient(tokenSet)
		info, err := portalClient.FetchEnvironmentInfoByHumanID(envConfig.HumanID)
		if err != nil {
			return err
		}
		portalInfo = info
		portalInfoJSON, err := json.MarshalIndent(portalInfo, "", "  ")
		if err != nil {
			return err
		}
		log.Debug().Msgf("Portal client info: %s", portalInfoJSON)
	}

	// Output based on format
	if o.flagFormat == "json" {
		// Pretty-print as JSON for full details
		envInfoJSON, err := json.MarshalIndent(envInfo, "", "  ")
		if err != nil {
			return err
		}
		log.Info().Msg(string(envInfoJSON))
	} else {
		deployment := envInfo.Deployment
		observability := envInfo.Observability
		oauth2Client := envInfo.OAuth2Client

		// Portal information (if available)
		if portalInfo != nil {
			log.Info().Msgf("")
			log.Info().Msgf("Portal information:")
			log.Info().Msgf("  Name:                 %s", styles.RenderTechnical(portalInfo.Name))
			log.Info().Msgf("  Human ID:             %s", styles.RenderTechnical(portalInfo.HumanID))
			log.Info().Msgf("  Environment family:   %s", styles.RenderTechnical(string(portalInfo.Type)))
			log.Info().Msgf("  Hosting type:         %s", styles.RenderTechnical(string(portalInfo.HostingType)))
			log.Info().Msgf("  Stack domain:         %s", styles.RenderTechnical(portalInfo.StackDomain))
		} else {
			log.Info().Msgf("Environment information not available in Metaplay portal")
		}
		log.Info().Msg("")

		// Print relevant information in text format
		log.Info().Msgf("Environment details:")
		log.Info().Msgf("  Admin hostname:       %s", styles.RenderTechnical(deployment.AdminHostname))
		log.Info().Msgf("  Server hostname:      %s", styles.RenderTechnical(deployment.ServerHostname))
		log.Info().Msgf("  Server ports:         %s", styles.RenderTechnical(intListToStr(deployment.ServerPorts)))
		log.Info().Msgf("  Kubernetes namespace: %s", styles.RenderTechnical(deployment.KubernetesNamespace))
		log.Info().Msgf("  AWS region:           %s", styles.RenderTechnical(deployment.AwsRegion))
		log.Info().Msgf("  Infra version:        %s", styles.RenderTechnical(deployment.MetaplayInfraVersion))
		log.Info().Msgf("")
		log.Info().Msgf("Observability:")
		log.Info().Msgf("  Prometheus endpoint:  %s", styles.RenderTechnical(observability.PrometheusEndpoint))
		log.Info().Msgf("  Loki endpoint:        %s", styles.RenderTechnical(observability.LokiEndpoint))
		log.Info().Msgf("")
		log.Info().Msgf("OAuth2 client:")
		log.Info().Msgf("  Domain:               %s", styles.RenderTechnical(oauth2Client.Domain))
		log.Info().Msgf("  Client ID:            %s", styles.RenderTechnical(oauth2Client.ClientID))
		log.Info().Msgf("  Email domain:         %s", styles.RenderTechnical(oauth2Client.EmailDomain))
		log.Info().Msgf("")

		// Database information
		// \todo Show high-level information like the database type (eg, local Maria vs Aurora RDS)
		shard0 := shards[0]
		shardBadge := styles.RenderMuted("[shard #0]")
		log.Info().Msgf("Database:")
		log.Info().Msgf("  Shards:               %s", styles.RenderTechnical(fmt.Sprintf("%d", len(shards))))
		log.Info().Msgf("  Database name:        %s", styles.RenderTechnical(shard0.DatabaseName))
		log.Info().Msgf("  Read-write host:      %s %s", styles.RenderTechnical(shard0.ReadWriteHost), shardBadge)
		log.Info().Msgf("  Read-only host:       %s %s", styles.RenderTechnical(shard0.ReadOnlyHost), shardBadge)
	}
	return nil
}
