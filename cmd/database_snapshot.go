/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// databaseSnapshotCmd is the parent command for cloud-managed database
// snapshot operations (list, create, delete). These are distinct from the
// application-level SQL dumps produced by 'database export-snapshot' and
// 'database import-snapshot'.
var databaseSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage cloud-managed database snapshots",
	Long: `Manage cloud-managed database snapshots for an environment.

These commands operate on the managed database cluster's native snapshots
(e.g. AWS RDS snapshots). They are a separate feature from the ad-hoc SQL
dumps produced by 'database export-snapshot' / 'database import-snapshot'.`,
}

func init() {
	databaseCmd.AddCommand(databaseSnapshotCmd)
}
