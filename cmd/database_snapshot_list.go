/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type databaseSnapshotListOpts struct {
	UsePositionalArgs

	argEnvironment string

	flagType   string
	flagLimit  int
	flagFormat string
}

func init() {
	o := databaseSnapshotListOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "list-snapshots [ENVIRONMENT] [flags]",
		Aliases: []string{"ls-snapshots"},
		Short:   "List cloud-managed database snapshots for an environment",
		Long: renderLong(&o, `
			List the cloud-managed database snapshots available for an environment
			across all database shards. Manual, automated (scheduled), and backup
			service snapshots can be filtered with --type.

			Results are sorted by creation time, newest first. For environments where
			database operations are not supported, the list is empty.

			{Arguments}
		`),
		Example: renderExample(`
			# List all snapshots for 'nimbly'
			metaplay database list-snapshots nimbly

			# Only show manual snapshots
			metaplay database list-snapshots nimbly --type=manual

			# Emit JSON for scripting
			metaplay database list-snapshots nimbly --format=json
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().StringVarP(&o.flagType, "type", "t", "", "Filter by snapshot type: manual, automated, or backup")
	cmd.Flags().IntVar(&o.flagLimit, "limit", 0, "Maximum snapshots per shard (0 = no limit)")
	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseSnapshotListOpts) Prepare(cmd *cobra.Command, args []string) error {
	if err := validateDatabaseFormat(o.flagFormat); err != nil {
		return err
	}
	switch o.flagType {
	case "",
		envapi.DatabaseSnapshotTypeManual,
		envapi.DatabaseSnapshotTypeAutomated,
		envapi.DatabaseSnapshotTypeBackup:
		// valid
	default:
		return clierrors.NewUsageErrorf("Invalid --type value %q", o.flagType).
			WithSuggestion("Use --type=manual, --type=automated, or --type=backup")
	}
	if o.flagLimit < 0 {
		return clierrors.NewUsageErrorf("Invalid --limit value %d", o.flagLimit).
			WithSuggestion("Use a positive integer, or omit --limit for no limit")
	}
	return nil
}

func (o *databaseSnapshotListOpts) Run(cmd *cobra.Command) error {
	envConfig, targetEnv, _, err := resolveEnvironmentForDatabaseOps(cmd.Context(), o.argEnvironment)
	if err != nil {
		return err
	}

	resp, err := targetEnv.ListDatabaseSnapshots(envapi.ListDatabaseSnapshotsOptions{
		Type:  o.flagType,
		Limit: o.flagLimit,
	})
	if err != nil {
		return mapDatabaseHTTPError(err, "list snapshots")
	}

	if o.flagFormat == databaseFormatJSON {
		return printDatabaseJSON(resp)
	}

	if len(resp.Snapshots) == 0 {
		log.Info().Msgf("%s No snapshots found for environment '%s'.",
			styles.RenderMuted("―"),
			styles.RenderTechnical(envConfig.Name))
		if o.flagType != "" {
			log.Info().Msgf("  (filter: --type=%s)", o.flagType)
		}
		return nil
	}

	renderSnapshotTable(resp.Snapshots)
	return nil
}

// renderSnapshotTable prints a simple aligned text table of snapshots to the
// info log. Column widths are computed from the actual data so short tables
// do not have excessive whitespace.
func renderSnapshotTable(snapshots []envapi.DatabaseSnapshot) {
	headers := []string{"IDENTIFIER", "TYPE", "SHARD", "CREATED", "AGE", "STATUS", "NAME"}
	rows := make([][]string, 0, len(snapshots))
	for _, s := range snapshots {
		rows = append(rows, []string{
			s.Identifier,
			s.Type,
			fmt.Sprintf("%d", s.ShardIndex),
			formatDatabaseTime(s.CreatedAt),
			formatDatabaseAge(s.CreatedAt),
			s.Status,
			s.Name,
		})
	}
	printDatabaseTable(headers, rows)
}

// printDatabaseTable prints a simple column-aligned table to the info log.
// Columns are separated by two spaces. Empty cells render as a single dash.
func printDatabaseTable(headers []string, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if cell == "" {
				cell = "-"
			}
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Header row.
	log.Info().Msgf("%s", styles.RenderTitle(padCells(headers, widths)))

	// Data rows.
	for _, row := range rows {
		cells := make([]string, len(row))
		copy(cells, row)
		for i := range cells {
			if cells[i] == "" {
				cells[i] = "-"
			}
		}
		log.Info().Msgf("%s", padCells(cells, widths))
	}
}

// padCells joins a row of cells with two-space separators, padding each cell
// to the corresponding column width (no padding on the last cell).
func padCells(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, cell := range cells {
		if i == len(cells)-1 {
			parts[i] = cell
		} else {
			parts[i] = cell + strings.Repeat(" ", widths[i]-len(cell))
		}
	}
	return strings.Join(parts, "  ")
}
