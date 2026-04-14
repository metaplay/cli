/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 *
 * Deprecated: Use 'database import-archive' instead.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// databaseImportSnapshotOpts holds the options for the deprecated 'database import-snapshot' command
type databaseImportSnapshotOpts struct {
	UsePositionalArgs

	argEnvironment        string
	argInputFile          string
	flagYes               bool
	flagForce             bool
	flagConfirmProduction bool
}

func init() {
	o := databaseImportSnapshotOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argInputFile, "INPUT_FILE", "Input file path containing database archive (eg, 'database-archive.mdb').")

	cmd := &cobra.Command{
		Use:        "import-snapshot [ENVIRONMENT] [INPUT_FILE] [flags]",
		Short:      "Import database archive from a file",
		Deprecated: "use 'metaplay database import-archive' instead.",
		Run:        runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompt and proceed with import")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with import even if a game server is deployed (DANGEROUS!)")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required flag when importing to production environments")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseImportSnapshotOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Delegate validation to the archive command
	archiveOpts := &databaseImportArchiveOpts{}
	archiveOpts.argEnvironment = o.argEnvironment
	archiveOpts.argInputFile = o.argInputFile
	archiveOpts.flagYes = o.flagYes
	archiveOpts.flagForce = o.flagForce
	archiveOpts.flagConfirmProduction = o.flagConfirmProduction
	return archiveOpts.Prepare(cmd, args)
}

func (o *databaseImportSnapshotOpts) Run(cmd *cobra.Command) error {
	// Delegate to the new import-archive command implementation
	archiveOpts := &databaseImportArchiveOpts{}
	archiveOpts.argEnvironment = o.argEnvironment
	archiveOpts.argInputFile = o.argInputFile
	archiveOpts.flagYes = o.flagYes
	archiveOpts.flagForce = o.flagForce
	archiveOpts.flagConfirmProduction = o.flagConfirmProduction
	return archiveOpts.Run(cmd)
}
