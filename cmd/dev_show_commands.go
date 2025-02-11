/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/spf13/cobra"
)

// Render the command tree
type showCommandsOpts struct {
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
}

func (o *showCommandsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	return nil
}

func (o *showCommandsOpts) Run(cmd *cobra.Command) error {
	printCommandTree(rootCmd, "")

	return nil
}

// printCommandTree recursively prints the command hierarchy
func printCommandTree(cmd *cobra.Command, indent string) {
	fmt.Printf("%s%s: %s\n", indent, styles.RenderTechnical(cmd.Name()), cmd.Short)
	for _, subCmd := range cmd.Commands() {
		printCommandTree(subCmd, indent+"  ")
	}
}
