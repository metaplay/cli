/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 *
 * Deprecated: Use 'database export-archive' instead.
 */

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/metaplay/cli/pkg/styles"
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
		Use:    "export-snapshot [ENVIRONMENT] [OUTPUT_FILE] [flags]",
		Short:  "Export database archive from an environment",
		Hidden: true,
		Run:    runCommand(&o),
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
	printDeprecationBanner("export-snapshot", "export-archive")

	// Delegate to the new export-archive command implementation
	archiveOpts := &databaseExportArchiveOpts{}
	archiveOpts.argEnvironment = o.argEnvironment
	archiveOpts.argOutputFile = o.argOutputFile
	archiveOpts.flagForce = o.flagForce
	return archiveOpts.Run(cmd)
}

// printDeprecationBanner prints a colored deprecation warning banner to stderr.
func printDeprecationBanner(oldCommand, newCommand string) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, styles.RenderWarning("  ============================================================"))
	fmt.Fprintln(os.Stderr, styles.RenderWarning(fmt.Sprintf("  WARNING: 'metaplay database %s' is deprecated.", oldCommand)))
	fmt.Fprintln(os.Stderr, styles.RenderWarning(fmt.Sprintf("  Use 'metaplay database %s' instead.", newCommand)))
	fmt.Fprintln(os.Stderr, styles.RenderWarning("  ============================================================"))
	fmt.Fprintln(os.Stderr, "")
}
