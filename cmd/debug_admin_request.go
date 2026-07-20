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

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// \todo Add a --no-retry flag to avoid HTTP retries on failed requests

// Command to make HTTP requests to the game server admin API.
type debugAdminRequestOpts struct {
	UsePositionalArgs

	argEnvironment  string
	argMethod       string
	argPath         string
	flagBody        string
	flagFile        string
	flagContentType string
	flagOutput      string
}

func init() {
	o := debugAdminRequestOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argMethod, "METHOD", "HTTP method to use: GET, POST, DELETE, PUT.")
	args.AddStringArgument(&o.argPath, "PATH", "Path for the admin API request, eg 'api/hello'.")

	cmd := &cobra.Command{
		Use:     "admin-request ENVIRONMENT METHOD PATH [flags]",
		Aliases: []string{"admin"},
		Short:   "Make HTTP requests to the game server admin API",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Make HTTP requests to the game server admin API.

			This command allows you to interact with the game server's admin API endpoint using
			various HTTP methods. You can pass a request body using either the --body flag for
			providing raw content directly or the --file flag to read content from a file.

			Requests are automatically retried a few times to mitigate transient network errors.

			{Arguments}

			Related commands:
			- 'metaplay debug server-status ...' checks the status of a game server deployment.
		`),
		Example: renderExample(`
			# Get the server hello message.
			metaplay debug admin-request nimbly GET api/hello

			# Pipe JSON output to jq for colored rendering.
			metaplay debug admin-request nimbly GET api/hello | jq

			# Send a POST request with request body from command line.
			metaplay debug admin-request nimbly POST api/some-endpoint --body '{"name":"test-resource"}'

			# Send a POST request with request body containing json data from command line.
			metaplay debug admin-request nimbly POST api/some-endpoint --content-type application/json --body '{"name":"test-resource"}'

			# Send a PUT request with request payload from file.
			metaplay debug admin-request nimbly PUT api/some-endpoint --file update.json

			# Send a DELETE request.
			metaplay debug admin-request nimbly DELETE api/some-endpoint

			# Download a binary file (e.g., game config archive).
			metaplay debug admin-request nimbly GET api/GameConfig/.../download -o gameconfig.mca
		`),
	}

	cmd.Flags().StringVar(&o.flagBody, "body", "", "Raw content to use as the request body")
	cmd.Flags().StringVar(&o.flagFile, "file", "", "Path to a file containing content to use as the request body")
	cmd.Flags().StringVar(&o.flagContentType, "content-type", "", "Content-Type passed as header for the API request, e.g. application/json. If not specific, automatically determined based on the `file` or `body` parameter")
	cmd.Flags().StringVarP(&o.flagOutput, "output", "o", "", "Save response to file (for binary/non-JSON downloads)")

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

	// Detect MSYS/Git-Bash path mangling: when a bash arg starts with '/', MSYS
	// rewrites it to a Windows path like "C:/Program Files/Git/api/hello". No
	// legitimate admin API path starts with a drive letter, so this is unambiguous.
	if len(o.argPath) >= 3 && o.argPath[1] == ':' &&
		(o.argPath[2] == '/' || o.argPath[2] == '\\') &&
		((o.argPath[0] >= 'A' && o.argPath[0] <= 'Z') || (o.argPath[0] >= 'a' && o.argPath[0] <= 'z')) {
		return clierrors.NewUsageErrorf("PATH argument looks like it was rewritten by MSYS/Git-Bash: %q", o.argPath).
			WithSuggestion(fmt.Sprintf("Drop the leading slash — the CLI adds it automatically. For example:\n  metaplay debug admin-request %s %s api/<your-path>", o.argEnvironment, o.argMethod)).
			WithDetails("MSYS/Git-Bash rewrites bash args starting with '/' into Windows paths. You can also prefix the invocation with MSYS_NO_PATHCONV=1 to disable this conversion.")
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

func IsJSON(str string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(str), &js) == nil
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

	// Admin hostname follows the infra-modules convention: <humanID>-admin.<stackDomain>.
	// Avoids a privileged StackAPI /v0/deployments call just to learn the public hostname.
	adminAPIBaseURL := fmt.Sprintf("https://%s-admin.%s", envConfig.HumanID, envConfig.StackDomain)
	adminClient := metahttp.NewJSONClient(tokenSet, adminAPIBaseURL)

	// Prepare request body if needed
	var requestBody any

	var contentType = o.flagContentType

	if o.flagBody != "" {
		// Use raw body content
		requestBody = o.flagBody
		// Detect if passed string is JSON, otherwise fallback to default resty behavior
		if o.flagContentType == "" && IsJSON(o.flagBody) {
			contentType = "application/json"
		} else if o.flagContentType == "application/json" && !IsJSON(o.flagBody) {
			log.Warn().Msg(styles.RenderWarning("⚠️ Content-Type is application/json but --body does not appear to be valid JSON"))
		}
	} else if o.flagFile != "" {
		// Read content from file
		fileContent, err := os.ReadFile(o.flagFile)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %v", o.flagFile, err)
		}
		requestBody = fileContent

		// Detect if file content is JSON, otherwise use octet-stream
		if o.flagContentType == "" {
			if IsJSON(string(fileContent)) {
				contentType = "application/json"
			} else {
				contentType = "application/octet-stream"
			}
		}
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

	// Binary download mode: save raw response to file
	if o.flagOutput != "" {
		request := adminClient.Resty.R()
		if contentType != "" {
			request.SetHeader("Content-Type", contentType)
		}
		if requestBody != nil {
			request.SetBody(requestBody)
		}

		response, err := request.Execute(o.argMethod, o.argPath)
		if err != nil {
			return fmt.Errorf("request failed: %v", err)
		}
		if response.StatusCode() < http.StatusOK || response.StatusCode() >= http.StatusMultipleChoices {
			return fmt.Errorf("request failed with status %d: %s", response.StatusCode(), response.String())
		}

		if err := os.WriteFile(o.flagOutput, response.Body(), 0644); err != nil {
			return fmt.Errorf("failed to write output file: %v", err)
		}

		log.Info().Msgf("Saved response to %s (%d bytes)", o.flagOutput, len(response.Body()))
		return nil
	}

	// JSON mode: make the HTTP request and unmarshal response
	var response any
	var requestErr error

	switch o.argMethod {
	case http.MethodGet:
		response, requestErr = metahttp.Get[any](adminClient, o.argPath)
	case http.MethodPost:
		response, requestErr = metahttp.Post[any](adminClient, o.argPath, requestBody, contentType)
	case http.MethodPut:
		response, requestErr = metahttp.Put[any](adminClient, o.argPath, requestBody, contentType)
	case http.MethodDelete:
		response, requestErr = metahttp.Delete[any](adminClient, o.argPath, requestBody, contentType)
	default:
		return fmt.Errorf("unsupported HTTP method: %s", o.argMethod)
	}

	if requestErr != nil {
		return fmt.Errorf("request failed: %v", requestErr)
	}

	// If no response body was returned, print something to acknowledge the result
	if response == nil {
		log.Info().Msg(styles.RenderSuccess("✅ Request successful!"))
		return nil
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
