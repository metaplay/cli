/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/metaplay/cli/pkg/styles"
)

var customUsageTemplate = `{{StyleHeading "Usage:"}}{{if .Runnable}}
  {{StyleCommand .UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{StyleCommand .CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{StyleHeading "Aliases:"}}
  {{StyleAliases .NameAndAliases}}{{end}}{{if .HasExample}}

{{StyleHeading "Examples:"}}
{{StyleExample .Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{StyleHeading "Available Commands:"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{StyleCommand (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{StyleHeading .Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{StyleCommand (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{StyleHeading "Additional Commands:"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{StyleCommand (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{StyleHeading "Flags:"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces | StyleFlags}}{{end}}{{if .HasAvailableInheritedFlags}}

{{StyleHeading "Global Flags:"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces | StyleFlags}}{{end}}{{if .HasHelpSubCommands}}

{{StyleHeading "Additional help topics:"}}{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

var customHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces | styleInlineCode}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

// Initialize the colored help templates for Cobra
func initColoredHelpTemplates(rootCmd *cobra.Command) {
	// Add template functions for styling
	cobra.AddTemplateFunc("StyleHeading", styles.RenderBright)
	cobra.AddTemplateFunc("StyleCommand", styles.RenderTechnical)
	cobra.AddTemplateFunc("StyleExample", styleExample)
	cobra.AddTemplateFunc("StyleFlags", styleFlags)
	cobra.AddTemplateFunc("StyleAliases", styleAliases)
	cobra.AddTemplateFunc("styleInlineCode", styleInlineCode)

	// Set the custom templates
	rootCmd.SetUsageTemplate(customUsageTemplate)
	rootCmd.SetHelpTemplate(customHelpTemplate)
}

func styleFlags(text string) string {
	lines := strings.Split(text, "\n")

	// Style each line separately
	for ndx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		// 1. Handle the indent first
		indentMatch := regexp.MustCompile(`^(\s*)(.*?)$`).FindStringSubmatch(line)
		if len(indentMatch) < 3 {
			continue
		}

		indent := indentMatch[1]
		rest := indentMatch[2]

		// 2. Split the left (flags + type) and right (description) part where there are at least two spaces
		parts := regexp.MustCompile(`^(.+?)(\s{2,})(.*)$`).FindStringSubmatch(rest)
		if len(parts) < 3 {
			// No description part or no double space
			continue
		}

		leftPart := parts[1] // flags + type
		space := parts[2]
		description := parts[3]

		// 3. Handle the flags + type with a regex
		flagsTypeMatch := regexp.MustCompile(`^((?:-[^,\s]+)(?:, (?:--[^\s]+))?)(?:\s+(\S+))?$`).FindStringSubmatch(leftPart)
		if len(flagsTypeMatch) < 2 {
			continue
		}

		flags := flagsTypeMatch[1]
		flagType := ""
		if len(flagsTypeMatch) > 2 && flagsTypeMatch[2] != "" {
			flagType = flagsTypeMatch[2]
		}

		// Style the flags
		flagParts := strings.Split(flags, ", ")
		for j, flag := range flagParts {
			flagParts[j] = styles.RenderTechnical(flag)
		}
		styledFlags := strings.Join(flagParts, ", ")

		// Style the type if present
		styledLeftPart := styledFlags
		if flagType != "" {
			styledType := styles.RenderMuted(flagType)
			styledLeftPart = styledFlags + " " + styledType
		}

		// 4. Combine it all
		lines[ndx] = fmt.Sprintf("%s%s%s%s", indent, styledLeftPart, space, description)
	}

	return strings.Join(lines, "\n")
}

// Style inline code (text between backticks) with color
func styleInlineCode(text string) string {
	// Find all occurrences of text between backticks (`) and color them
	parts := strings.Split(text, "`")
	for i := 1; i < len(parts); i += 2 {
		if i < len(parts) {
			// Color only the odd-indexed parts (the ones between backticks)
			parts[i] = styles.RenderTechnical(parts[i])
		}
	}

	// Rejoin with backticks
	var result strings.Builder
	for i, part := range parts {
		if i > 0 {
			result.WriteString("`")
		}
		result.WriteString(part)
	}
	return result.String()
}

// Style examples with different colors for comment and command lines
func styleExample(text string) string {
	lines := strings.Split(text, "\n")

	for ndx, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "#") {
			// This is a comment line, style it with RenderComment (darker green)
			lines[ndx] = styles.RenderComment(line)
		} else if trimmedLine != "" {
			// This is a command line, style it with RenderTechnical (blue)
			lines[ndx] = styles.RenderTechnical(line)
		} else {
			// Empty line, leave as is
		}
	}
	return strings.Join(lines, "\n")
}

func styleAliases(text string) string {
	// Split the aliases by comma and space
	parts := strings.Split(text, ", ")

	// Style each alias
	for i, alias := range parts {
		parts[i] = styles.RenderTechnical(alias)
	}

	// Rejoin with comma and space
	return strings.Join(parts, ", ")
}
