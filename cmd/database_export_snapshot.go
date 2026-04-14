/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 *
 * Deprecated: Use 'database export-archive' instead.
 */

package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// databaseExportSnapshotOpts holds the options for the deprecated 'database export-snapshot' command
type databaseExportSnapshotOpts struct {
	UsePositionalArgs

	argEnvironment string
	argOutputFile  string
	flagForce      bool
}

func init() {
	o := databaseExportSnapshotOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argOutputFile, "OUTPUT_FILE", "Output file path for the database archive.")

	cmd := &cobra.Command{
		Use:        "export-snapshot [ENVIRONMENT] [OUTPUT_FILE] [flags]",
		Short:      "Export database archive from an environment",
		Deprecated: "use 'metaplay database export-archive' instead.",
		Run:        runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with export even if a game server is deployed (DANGEROUS!)")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseExportSnapshotOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.argOutputFile == "" {
		timestamp := time.Now().Format("20060102-150405")
		o.argOutputFile = fmt.Sprintf("database-archive-%s-%s.mdb", o.argEnvironment, timestamp)
	}
	return nil
}

func (o *databaseExportSnapshotOpts) Run(cmd *cobra.Command) error {
	// Delegate to the new export-archive command implementation
	archiveOpts := &databaseExportArchiveOpts{}
	archiveOpts.argEnvironment = o.argEnvironment
	archiveOpts.argOutputFile = o.argOutputFile
	archiveOpts.flagForce = o.flagForce
	return archiveOpts.Run(cmd)
}
