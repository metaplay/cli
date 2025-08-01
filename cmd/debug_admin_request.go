/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command to make HTTP requests to the game server admin API.
type debugAdminRequestOpts struct {
	UsePositionalArgs

	argEnvironment string
	argMethod      string
	argPath        string
	flagBody       string
	flagFile       string
}

func init() {
	o := debugAdminRequestOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argMethod, "METHOD", "HTTP method to use: GET, POST, DELETE, PUT.")
	args.AddStringArgument(&o.argPath, "PATH", "Path for the admin API request, eg '/api/v1/status'.")

	cmd := &cobra.Command{
		Use:     "admin-request ENVIRONMENT METHOD PATH [flags]",
		Aliases: []string{"admin"},
		Short:   "[preview] Make HTTP requests to the game server admin API",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Make HTTP requests to the game server admin API.

			This command allows you to interact with the game server's admin API endpoint using
			various HTTP methods. You can pass a request body using either the --body flag for
			providing raw content directly or the --file flag to read content from a file.

			{Arguments}

			Related commands:
			- 'metaplay debug server-status ...' checks the status of a game server deployment.
		`),
		Example: renderExample(`
			# Get the server hello message.
			metaplay debug admin-request tough-falcons GET /api/hello

			# Pipe JSON output to jq for colored rendering.
			metaplay debug admin-request tough-falcons GET /api/hello | jq

			# Send a POST request with request body from command line.
			metaplay debug admin-request tough-falcons POST /api/some-endpoint --body '{"name":"test-resource"}'

			# Send a PUT request with request payload from file.
			metaplay debug admin-request tough-falcons PUT /api/some-endpoint --file update.json

			# Send a DELETE request.
			metaplay debug admin-request tough-falcons DELETE /api/some-endpoint
		`),
	}

	cmd.Flags().StringVar(&o.flagBody, "body", "", "Raw content to use as the request body")
	cmd.Flags().StringVar(&o.flagFile, "file", "", "Path to a file containing content to use as the request body")

	debugCmd.AddCommand(cmd)
}

func (o *debugAdminRequestOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate HTTP method
	o.argMethod = strings.ToUpper(o.argMethod)
	validMethods := map[string]bool{
		http.MethodGet:    true,
		http.MethodPost:   true,
		http.MethodDelete: true,
		http.MethodPut:    true,
	}

	if !validMethods[o.argMethod] {
		return fmt.Errorf("invalid HTTP method: %s. Must be one of: GET, POST, DELETE, PUT", o.argMethod)
	}

	// Ensure path starts with a slash
	if !strings.HasPrefix(o.argPath, "/") {
		o.argPath = "/" + o.argPath
	}

	// Check that only one body source is provided
	if o.flagBody != "" && o.flagFile != "" {
		return fmt.Errorf("only one of --body or --file can be specified")
	}

	// Validate body for methods that accept it
	if (o.argMethod == http.MethodPost || o.argMethod == http.MethodPut) &&
		o.flagBody == "" && o.flagFile == "" {
		log.Warn().Msg("Making a POST/PUT request without a request body")
	}

	return nil
}

func (o *debugAdminRequestOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve project and environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get environment details for admin API hostname
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Create a client for the game server admin API
	adminAPIBaseURL := fmt.Sprintf("https://%s", envDetails.Deployment.AdminHostname)
	adminClient := metahttp.NewJSONClient(tokenSet, adminAPIBaseURL)

	// Prepare request body if needed
	var requestBody any

	if o.flagBody != "" {
		// Use raw body content
		requestBody = o.flagBody
	} else if o.flagFile != "" {
		// Read content from file
		fileContent, err := os.ReadFile(o.flagFile)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %v", o.flagFile, err)
		}
		requestBody = string(fileContent)
	}

	// Debug logging
	log.Debug().Msg("")
	log.Debug().Msg(styles.RenderTitle("Admin API Request"))
	log.Debug().Msg("")
	log.Debug().Msgf("Target environment:")
	log.Debug().Msgf("  Name:            %s", styles.RenderTechnical(envConfig.Name))
	log.Debug().Msgf("  ID:              %s", styles.RenderTechnical(envConfig.HumanID))
	log.Debug().Msgf("  Admin API:       %s", styles.RenderTechnical(adminAPIBaseURL))
	log.Debug().Msg("Request details:")
	log.Debug().Msgf("  Method:          %s", styles.RenderTechnical(o.argMethod))
	log.Debug().Msgf("  Path:            %s", styles.RenderTechnical(o.argPath))
	if o.flagBody != "" {
		log.Debug().Msgf("  Body (raw):       %s", styles.RenderTechnical(o.flagBody))
	} else if o.flagFile != "" {
		log.Debug().Msgf("  Body (from file): %s", styles.RenderTechnical(o.flagFile))
	}
	log.Debug().Msg("")

	// Make the HTTP request based on the method
	var response any
	var requestErr error

	switch o.argMethod {
	case http.MethodGet:
		response, requestErr = metahttp.Get[any](adminClient, o.argPath)
	case http.MethodPost:
		response, requestErr = metahttp.Post[any](adminClient, o.argPath, requestBody)
	case http.MethodPut:
		response, requestErr = metahttp.Put[any](adminClient, o.argPath, requestBody)
	case http.MethodDelete:
		response, requestErr = metahttp.Delete[any](adminClient, o.argPath, requestBody)
	default:
		return fmt.Errorf("unsupported HTTP method: %s", o.argMethod)
	}

	if requestErr != nil {
		return fmt.Errorf("request failed: %v", requestErr)
	}

	// Format and display the response
	log.Debug().Msg(styles.RenderSuccess("✅ Request successful!"))
	log.Debug().Msg("")
	log.Debug().Msg("Response:")

	// Pretty-print the JSON response
	prettyJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		log.Info().Msgf("%v", response)
	} else {
		log.Info().Msg(string(prettyJSON))
	}

	return nil
}
