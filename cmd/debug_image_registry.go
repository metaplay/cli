/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
)

// Report both paths to an environment's images, and which one a push takes.
type debugImageRegistryOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
}

func init() {
	o := debugImageRegistryOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:   "image-registry ENVIRONMENT [flags]",
		Short: "Report how the CLI reaches an environment's images",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Report both paths the CLI can take to an environment's image repository,
			what each of them answered, and which one a push or deploy would take.

			A stack issues a credential for an environment's images from its image
			registry credentials endpoint. A stack older than that endpoint does not
			serve it, and the CLI then reaches the repository the environment's
			description names, in ECR, with AWS credentials the stack issues. Which
			path a command took is otherwise invisible: it shows only whatever failed
			afterwards.

			Both paths are always asked. A path that fails is reported rather than
			stopping the command, so it exits successfully whatever they answered.
			The credential's expiry is shown where it states one. The password is
			never shown.

			Needs no deployed game server: it talks to the stack's API alone.

			{Arguments}

			Related commands:
			- 'metaplay image push ...' pushes an image to the environment's repository.
			- 'metaplay image list ...' lists the images in it.
		`),
		Example: renderExample(`
			# Report how the CLI reaches the images of environment 'nimbly'.
			metaplay debug image-registry nimbly

			# The same report as JSON.
			metaplay debug image-registry nimbly --format=json
		`),
	}

	debugCmd.AddCommand(cmd)

	cmd.Flags().StringVar(&o.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'")
}

func (o *debugImageRegistryOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return clierrors.NewUsageErrorf("Invalid format '%s'", o.flagFormat).
			WithSuggestion("Use --format=text or --format=json")
	}
	return nil
}

func (o *debugImageRegistryOpts) Run(cmd *cobra.Command) error {
	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	probe := targetEnv.ProbeImageRegistry()

	output, err := formatImageRegistryProbe(probe, o.flagFormat, time.Now())
	if err != nil {
		return err
	}
	if o.flagFormat == "text" {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle("Image Registry Paths"))
		log.Info().Msg("")
		log.Info().Msgf("Target environment:")
		log.Info().Msgf("  Name:              %s", styles.RenderTechnical(envConfig.Name))
		log.Info().Msgf("  ID:                %s", styles.RenderTechnical(envConfig.HumanID))
		log.Info().Msgf("  Stack domain:      %s", styles.RenderTechnical(envConfig.StackDomain))
		log.Info().Msg("")
	}
	for line := range strings.SplitSeq(output, "\n") {
		log.Info().Msg(line)
	}
	return nil
}

// formatImageRegistryProbe is the probe as the command prints it, with each
// expiry measured against now.
func formatImageRegistryProbe(probe *envapi.ImageRegistryProbe, format string, now time.Time) (string, error) {
	if format == "json" {
		encoded, err := json.MarshalIndent(probe, "", "  ")
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}

	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	field := func(name, value string) { add("  %-17s  %s", name+":", value) }

	endpoint := probe.CredentialsEndpoint
	add("Registry credentials endpoint: %s", renderEndpointStatus(endpoint.Status))
	switch endpoint.Status {
	case envapi.CredentialsEndpointServed:
		field("Registry host", styles.RenderTechnical(endpoint.RegistryHost))
		field("Repository", styles.RenderTechnical(endpoint.Repository))
		field("Username", styles.RenderTechnical(endpoint.Username))
		if endpoint.ExpiresAt != nil {
			field("Expires", renderExpiry(*endpoint.ExpiresAt, now))
		} else {
			field("Expires", styles.RenderMuted("not stated by the credential (an ECR token is valid for 12 hours)"))
		}
	case envapi.CredentialsEndpointNotServed:
		field("Meaning", styles.RenderMuted("the stack predates the endpoint"))
	case envapi.CredentialsEndpointFailed:
		field("Error", styles.RenderError(endpoint.Error))
	}
	add("")

	older := probe.OlderPath
	add("Older path (the environment's ECR repository): %s", renderOlderPathStatus(older.Status))
	switch older.Status {
	case envapi.OlderPathReachable:
		field("Repository", styles.RenderTechnical(older.Repository))
		if older.ExpiresAt != nil {
			field("Expires", renderExpiry(*older.ExpiresAt, now))
		}
	case envapi.OlderPathAbsent:
		field("Meaning", styles.RenderMuted(older.Error))
	case envapi.OlderPathFailed:
		field("Error", styles.RenderError(older.Error))
	}
	add("")

	switch probe.PushPath {
	case envapi.PushPathCredentialsEndpoint:
		add("A push or deploy would take: %s", styles.RenderSuccess("the registry credentials endpoint"))
	case envapi.PushPathOlder:
		add("A push or deploy would take: %s", styles.RenderSuccess("the older path"))
	default:
		add("A push or deploy would take: %s", styles.RenderError("neither"))
		field("It would fail with", styles.RenderError(probe.PushPathError))
	}
	return strings.Join(lines, "\n"), nil
}

func renderEndpointStatus(status envapi.CredentialsEndpointStatus) string {
	switch status {
	case envapi.CredentialsEndpointServed:
		return styles.RenderSuccess("served")
	case envapi.CredentialsEndpointNotServed:
		return styles.RenderMuted("not served")
	default:
		return styles.RenderError("failed")
	}
}

func renderOlderPathStatus(status envapi.OlderPathStatus) string {
	switch status {
	case envapi.OlderPathReachable:
		return styles.RenderSuccess("reachable")
	case envapi.OlderPathAbsent:
		return styles.RenderMuted("absent")
	default:
		return styles.RenderError("failed")
	}
}

// renderExpiry is an expiry as an absolute time and as a distance from now,
// which is the one a developer returning to a refused push needs.
func renderExpiry(expiresAt, now time.Time) string {
	absolute := expiresAt.UTC().Format(time.RFC3339)
	remaining := expiresAt.Sub(now).Round(time.Second)
	if remaining <= 0 {
		return styles.RenderError(fmt.Sprintf("%s (expired %s ago)", absolute, -remaining))
	}
	return styles.RenderTechnical(absolute) + styles.RenderMuted(fmt.Sprintf(" (in %s)", remaining))
}
