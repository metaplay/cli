/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type exportCliReferenceOpts struct {
	flagFormat string
	flagOutput string
}

type cliReferenceOutput struct {
	Version  string                `json:"version"`
	Commands []cliReferenceCommand `json:"commands"`
}

type cliReferenceCommand struct {
	Path           []string           `json:"path"`
	Usage          string             `json:"usage"`
	Summary        string             `json:"summary"`
	Description    string             `json:"description,omitempty"`
	Examples       []cliReferenceItem `json:"examples"`
	Flags          []cliReferenceFlag `json:"flags"`
	InheritedFlags []cliReferenceFlag `json:"inheritedFlags"`
	Aliases        []string           `json:"aliases"`
	IsGroup        bool               `json:"isGroup,omitempty"`
	Hidden         bool               `json:"hidden,omitempty"`
}

type cliReferenceItem struct {
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
}

type cliReferenceFlag struct {
	Name         string `json:"name"`
	Shorthand    string `json:"shorthand,omitempty"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	EnvVar       string `json:"envVar,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Required     bool   `json:"required,omitempty"`
}

var ansiSequencePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var flagEnvVarPattern = regexp.MustCompile(`\s*\[env:\s*([^\]]+)\]\s*$`)

func init() {
	o := exportCliReferenceOpts{}

	cmd := &cobra.Command{
		Use:   "export-cli-reference",
		Short: "Export CLI command reference",
		Long: renderLong(&o, `
			Export full CLI command reference in machine-readable format.

			This command exports all visible commands and flags into a JSON file.
		`),
		Example: renderExample(`
			# Export CLI reference as JSON.
			metaplay dev export-cli-reference --format=json --output=cli-reference.json
		`),
		Run: runCommand(&o),
	}

	devCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "json", "Output format. Valid value is 'json'.")
	flags.StringVar(&o.flagOutput, "output", "", "Output file path (required)")
}

func (o *exportCliReferenceOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagFormat != "json" {
		return clierrors.NewUsageErrorf("Invalid format '%s'", o.flagFormat).
			WithSuggestion("Use --format=json")
	}

	if strings.TrimSpace(o.flagOutput) == "" {
		return clierrors.NewUsageError("Missing required flag --output").
			WithSuggestion("Provide output file path with --output=<path>")
	}

	return nil
}

func (o *exportCliReferenceOpts) Run(cmd *cobra.Command) error {
	output := buildCliReferenceOutput(rootCmd)

	content, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal CLI reference as JSON: %w", err)
	}

	content = append(content, '\n')

	if err := os.WriteFile(o.flagOutput, content, 0644); err != nil {
		return fmt.Errorf("failed to write CLI reference to file: %w", err)
	}

	log.Info().Msgf("✅ Wrote CLI reference to %s", styles.RenderTechnical(o.flagOutput))
	return nil
}

func buildCliReferenceOutput(root *cobra.Command) cliReferenceOutput {
	commands := collectVisibleCommands(root, []string{root.Name()})
	return cliReferenceOutput{
		Version:  version.AppVersion,
		Commands: commands,
	}
}

func collectVisibleCommands(cmd *cobra.Command, path []string) []cliReferenceCommand {
	if cmd.Hidden {
		return nil
	}

	entry := cliReferenceCommand{
		Path:           slicesClone(path),
		Usage:          sanitizeText(cmd.UseLine()),
		Summary:        sanitizeText(cmd.Short),
		Description:    sanitizeText(resolveCommandDescription(cmd)),
		Examples:       parseExamples(cmd.Example),
		Flags:          collectOwnedFlags(cmd),
		InheritedFlags: collectFlags(cmd.InheritedFlags()),
		Aliases:        slicesClone(cmd.Aliases),
	}

	visibleSubCommands := make([]*cobra.Command, 0)
	for _, subCmd := range cmd.Commands() {
		if !subCmd.Hidden {
			visibleSubCommands = append(visibleSubCommands, subCmd)
		}
	}
	entry.IsGroup = len(visibleSubCommands) > 0

	results := []cliReferenceCommand{entry}
	for _, subCmd := range visibleSubCommands {
		nextPath := append(slicesClone(path), subCmd.Name())
		results = append(results, collectVisibleCommands(subCmd, nextPath)...)
	}

	return results
}

func resolveCommandDescription(cmd *cobra.Command) string {
	if cmd.Long != "" {
		return cmd.Long
	}
	return cmd.Short
}

func collectOwnedFlags(cmd *cobra.Command) []cliReferenceFlag {
	if cmd == nil {
		return []cliReferenceFlag{}
	}

	// NonInheritedFlags includes flags declared on this command:
	// local flags + persistent flags owned by this command.
	return collectFlags(cmd.NonInheritedFlags())
}

func collectFlags(flagSet *pflag.FlagSet) []cliReferenceFlag {
	if flagSet == nil {
		return []cliReferenceFlag{}
	}

	flags := make([]cliReferenceFlag, 0)
	flagSet.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}

		description, envVar := extractFlagDescriptionAndEnvVar(flag.Usage)
		entry := cliReferenceFlag{
			Name:        flag.Name,
			Type:        flag.Value.Type(),
			Description: sanitizeText(description),
		}

		if flag.Shorthand != "" {
			entry.Shorthand = flag.Shorthand
		}
		if envVar != "" {
			entry.EnvVar = envVar
		}
		if flag.DefValue != "" {
			entry.DefaultValue = flag.DefValue
		}
		if isFlagRequired(flag) {
			entry.Required = true
		}

		flags = append(flags, entry)
	})

	if flags == nil {
		return []cliReferenceFlag{}
	}

	return flags
}

func isFlagRequired(flag *pflag.Flag) bool {
	if flag == nil || flag.Annotations == nil {
		return false
	}

	required, exists := flag.Annotations[cobra.BashCompOneRequiredFlag]
	if !exists || len(required) == 0 {
		return false
	}

	return required[0] == "true"
}

func parseExamples(raw string) []cliReferenceItem {
	trimmed := strings.TrimSpace(sanitizeText(raw))
	if trimmed == "" {
		return []cliReferenceItem{}
	}

	lines := strings.Split(trimmed, "\n")
	examples := make([]cliReferenceItem, 0)
	currentDescription := ""
	currentCommandLines := make([]string, 0)

	flush := func() {
		if len(currentCommandLines) == 0 {
			return
		}
		examples = append(examples, cliReferenceItem{
			Description: strings.TrimSpace(currentDescription),
			Command:     strings.TrimSpace(strings.Join(currentCommandLines, "\n")),
		})
		currentDescription = ""
		currentCommandLines = currentCommandLines[:0]
	}

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			flush()
			continue
		}

		if strings.HasPrefix(trimmedLine, "#") {
			flush()
			currentDescription = strings.TrimSpace(strings.TrimPrefix(trimmedLine, "#"))
			continue
		}

		currentCommandLines = append(currentCommandLines, strings.TrimRight(line, "\t "))
	}

	flush()

	if len(examples) == 0 {
		return []cliReferenceItem{{Command: trimmed}}
	}

	return examples
}

func extractFlagDescriptionAndEnvVar(usage string) (description, envVar string) {
	matches := flagEnvVarPattern.FindStringSubmatch(usage)
	if len(matches) < 2 {
		return usage, ""
	}

	envVar = strings.TrimSpace(matches[1])
	description = strings.TrimSpace(flagEnvVarPattern.ReplaceAllString(usage, ""))
	return description, envVar
}

func sanitizeText(str string) string {
	str = ansiSequencePattern.ReplaceAllString(str, "")
	return strings.TrimSpace(str)
}

func slicesClone(list []string) []string {
	if len(list) == 0 {
		return []string{}
	}
	cloned := make([]string, len(list))
	copy(cloned, list)
	return cloned
}
