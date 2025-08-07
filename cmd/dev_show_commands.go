/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
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
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create documentation file: %w", err)
	}
	defer file.Close()

	// Write introduction
	_, err = file.WriteString(`# Metaplay CLI Command Reference

This document provides full reference documentation of all available commands in the Metaplay CLI.

## Command Structure

The Metaplay CLI follows a hierarchical command structure with the root command 'metaplay'
followed by subcommands and their respective options and arguments.

## Available Commands

`)
	if err != nil {
		return fmt.Errorf("failed to write introduction: %w", err)
	}

	// Generate documentation for all visible commands
	err = o.writeCommandDocs(file, rootCmd, 0)
	if err != nil {
		return fmt.Errorf("failed to write command documentation: %w", err)
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ Wrote command reference to %s", styles.RenderTechnical(filename))
	return nil
}

// writeCommandDocs recursively writes documentation for a command and its subcommands
func (o *showCommandsOpts) writeCommandDocs(file *os.File, cmd *cobra.Command, depth int) error {
	// Skip hidden commands
	if cmd.Hidden {
		return nil
	}

	// Write command header
	cmdPath := o.getCommandPath(cmd)
	_, err := file.WriteString(fmt.Sprintf("### %s\n\n", cmdPath))
	if err != nil {
		return err
	}

	// Write description
	if cmd.Short != "" {
		_, err = file.WriteString(fmt.Sprintf("**Description:** %s\n\n", cmd.Short))
		if err != nil {
			return err
		}
	}

	// Write usage
	if cmd.Use != "" {
		_, err = file.WriteString(fmt.Sprintf("**Usage:**\n\n```shell\n%s\n```\n\n", cmd.UseLine()))
		if err != nil {
			return err
		}
	}

	// Write aliases if any
	if len(cmd.Aliases) > 0 {
		_, err = file.WriteString(fmt.Sprintf("**Aliases:** %s\n\n", strings.Join(cmd.Aliases, ", ")))
		if err != nil {
			return err
		}
	}

	// Write long description if available
	if cmd.Long != "" {
		_, err = file.WriteString(fmt.Sprintf("**Detailed Description:**\n\n%s\n\n", cmd.Long))
		if err != nil {
			return err
		}
	}

	// Write examples if available
	if cmd.Example != "" {
		_, err = file.WriteString(fmt.Sprintf("**Examples:**\n\n```shell\n%s\n```\n\n", cmd.Example))
		if err != nil {
			return err
		}
	}

	// Write flags if any
	if cmd.HasAvailableFlags() {
		_, err = file.WriteString("**Flags:**\n\n")
		if err != nil {
			return err
		}

		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if !flag.Hidden {
				flagDesc := fmt.Sprintf("- `--%s", flag.Name)
				if flag.Shorthand != "" {
					flagDesc += fmt.Sprintf(", -%s", flag.Shorthand)
				}
				if flag.Value.Type() != "bool" {
					flagDesc += fmt.Sprintf(" <%s>", flag.Value.Type())
				}
				flagDesc += fmt.Sprintf("`**: %s", flag.Usage)
				if flag.DefValue != "" && flag.DefValue != "false" {
					flagDesc += fmt.Sprintf(" (default: %s)", flag.DefValue)
				}
				flagDesc += "\n"
				file.WriteString(flagDesc)
			}
		})
		_, err = file.WriteString("\n")
		if err != nil {
			return err
		}
	}

	// Add separator between commands
	_, err = file.WriteString("---\n\n")
	if err != nil {
		return err
	}

	// Recursively process subcommands
	for _, subCmd := range cmd.Commands() {
		err = o.writeCommandDocs(file, subCmd, depth+1)
		if err != nil {
			return err
		}
	}

	return nil
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
