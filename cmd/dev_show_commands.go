/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Render the command tree
type showCommandsOpts struct {
	flagOutputDocs string
}

func init() {
	o := showCommandsOpts{}

	cmd := &cobra.Command{
		Use:   "show-commands",
		Short: "Show all the commands in the CLI",
		Run:   runCommand(&o),
	}

	cmd.Hidden = true
	devCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagOutputDocs, "output-docs", "", "EXPERIMENTAL: Output full command documentation as markdown to the specified file")
}

func (o *showCommandsOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *showCommandsOpts) Run(cmd *cobra.Command) error {
	// Print the command tree.
	printCommandTree(rootCmd, "")

	// Write command reference to a markdown file, if requested.
	if o.flagOutputDocs != "" {
		err := o.generateMarkdownDocs(o.flagOutputDocs)
		if err != nil {
			return err
		}
	}

	return nil
}

// generateMarkdownDocs generates comprehensive markdown documentation for all visible commands
func (o *showCommandsOpts) generateMarkdownDocs(filename string) error {
	// Generate documentation into a buffer first
	var buf bytes.Buffer

	// Write frontmatter and introduction
	_, err := buf.WriteString(`---
title: CLI Command Reference
description: Complete reference for all Metaplay CLI commands and their usage.
---

<!-- This file is auto-generated using the Metaplay CLI. DO NOT EDIT MANUALLY!! -->
<!-- To regenerate this file, run: metaplay dev show-commands --output-docs <filename> -->

<!-- markdownlint-disable MD007 --> <!-- Unordered list indentation -->
<!-- markdownlint-disable MD010 --> <!-- Hard tabs -->
<!-- markdownlint-disable MD012 --> <!-- Multiple consecutive blank lines -->
<!-- markdownlint-disable MD024 --> <!-- Duplicate headers -->
<!-- markdownlint-disable MD026 --> <!-- Colons in headers -->
<!-- markdownlint-disable MD029 --> <!-- Ordered list item prefix -->
<!-- markdownlint-disable MD032 --> <!-- Lists should be surrounded by blank lines -->
<!-- markdownlint-disable MD034 --> <!-- Allow bare URLs -->

`)
	if err != nil {
		return fmt.Errorf("failed to write introduction to buffer: %w", err)
	}

	// Generate documentation for all visible commands
	err = o.writeCommandDocs(&buf, rootCmd, 0)
	if err != nil {
		return fmt.Errorf("failed to write command documentation: %w", err)
	}

	// Replace tabs with two spaces
	content := strings.ReplaceAll(buf.String(), "\t", "  ")

	// Remove double empty lines
	content = strings.ReplaceAll(content, "\n\n\n", "\n\n")

	// Now write the processed content to file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create documentation file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("failed to write documentation to file: %w", err)
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ Wrote command reference to %s", styles.RenderTechnical(filename))
	return nil
}

// writeCommandDocs recursively writes documentation for a command and its subcommands
func (o *showCommandsOpts) writeCommandDocs(writer io.Writer, cmd *cobra.Command, depth int) error {
	// Skip hidden commands
	if cmd.Hidden {
		return nil
	}

	// Add separator between commands
	_, err := fmt.Fprintf(writer, "---\n\n")
	if err != nil {
		return err
	}

	// Write command header
	if cmd.Use == "" {
		panic("command has no use?!")
	}
	_, err = fmt.Fprintf(writer, "### `%s`\n\n", cmd.UseLine())
	if err != nil {
		return err
	}

	// Write long description if available
	cmdDescription := ""
	if cmd.Long != "" {
		cmdDescription = cmd.Long
	} else if cmd.Short != "" {
		cmdDescription = cmd.Short
	}
	if cmdDescription != "" {
		_, err = fmt.Fprintf(writer, "%s\n\n", escapeMarkdownCharacters(cmdDescription))
		if err != nil {
			return err
		}
	}

	// Write aliases if any
	if len(cmd.Aliases) > 0 {
		escapedAliases := make([]string, len(cmd.Aliases))
		for i, alias := range cmd.Aliases {
			escapedAliases[i] = "`" + escapeMarkdownCharacters(alias) + "`"
		}
		_, err = fmt.Fprintf(writer, "#### Aliases\n\n%s\n\n", strings.Join(escapedAliases, ", "))
		if err != nil {
			return err
		}
	}

	// Write examples if available
	if cmd.Example != "" {
		_, err = fmt.Fprintf(writer, "#### Examples\n\n```shell\n%s\n```\n\n", trimIndent(escapeMarkdownCharacters(cmd.Example), 0))
		if err != nil {
			return err
		}
	}

	// Write flags if any
	if cmd.HasAvailableFlags() {
		_, err = fmt.Fprintf(writer, "#### Flags\n\n")
		if err != nil {
			return err
		}

		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if !flag.Hidden {
				flagDesc := fmt.Sprintf("- **`--%s", escapeMarkdownCharacters(flag.Name))
				if flag.Shorthand != "" {
					flagDesc += fmt.Sprintf(", -%s", escapeMarkdownCharacters(flag.Shorthand))
				}
				if flag.Value.Type() != "bool" {
					flagDesc += fmt.Sprintf(" <%s>", escapeMarkdownCharacters(flag.Value.Type()))
				}
				flagDesc += fmt.Sprintf("`**: %s", escapeMarkdownCharacters(flag.Usage))
				if flag.DefValue != "" && flag.DefValue != "false" {
					flagDesc += fmt.Sprintf(" (default: %s)", escapeMarkdownCharacters(flag.DefValue))
				}
				flagDesc += "\n"
				fmt.Fprint(writer, flagDesc)
			}
		})
		_, err = fmt.Fprintf(writer, "\n")
		if err != nil {
			return err
		}
	}

	// Recursively process subcommands
	for _, subCmd := range cmd.Commands() {
		err = o.writeCommandDocs(writer, subCmd, depth+1)
		if err != nil {
			return err
		}
	}

	return nil
}

// escapeMarkdownCharacters escapes markdown special characters with backslashes
func escapeMarkdownCharacters(s string) string {
	// Escape markdown special characters
	s = strings.ReplaceAll(s, "<", "\\<")
	s = strings.ReplaceAll(s, ">", "\\>")
	return s
}

// getCommandPath returns the full command path (e.g., "metaplay build image")
func (o *showCommandsOpts) getCommandPath(cmd *cobra.Command) string {
	if cmd.Parent() == nil {
		return cmd.Name()
	}
	return o.getCommandPath(cmd.Parent()) + " " + cmd.Name()
}

// printCommandTree recursively prints the command hierarchy
func printCommandTree(cmd *cobra.Command, indent string) {
	log.Info().Msgf("%s%s: %s", indent, styles.RenderTechnical(cmd.Name()), cmd.Short)
	for _, subCmd := range cmd.Commands() {
		printCommandTree(subCmd, indent+"  ")
	}
}
