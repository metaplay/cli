/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// Output format constants used by database commands that support --format.
const (
	databaseFormatText = "text"
	databaseFormatJSON = "json"
)

// validateDatabaseFormat checks a --format flag value and returns a usage
// error if it is not one of the supported formats.
func validateDatabaseFormat(flagFormat string) error {
	switch flagFormat {
	case databaseFormatText, databaseFormatJSON:
		return nil
	default:
		return clierrors.NewUsageErrorf("Invalid format %q", flagFormat).
			WithSuggestion("Use --format=text for human-readable output or --format=json for scripting")
	}
}

// printDatabaseJSON marshals the given value as indented JSON and writes it to
// stdout. Used by commands that support --format=json.
func printDatabaseJSON(value any) error {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return clierrors.Wrap(err, "Failed to marshal output as JSON")
	}
	fmt.Println(string(out))
	return nil
}

// mapDatabaseHTTPError converts a *metahttp.HTTPError returned from the
// database operations API into a user-friendly *clierrors.CLIError with a
// context-aware message and suggestion. Non-HTTP errors are wrapped directly.
// operationDescription should complete the sentence "Failed to ...", e.g.
// "create snapshot", "delete snapshot", "rollback database".
func mapDatabaseHTTPError(err error, operationDescription string) error {
	if err == nil {
		return nil
	}
	var httpErr *metahttp.HTTPError
	if !errors.As(err, &httpErr) {
		return clierrors.Wrapf(err, "Failed to %s", operationDescription)
	}
	msg := httpErr.Message
	switch httpErr.StatusCode {
	case http.StatusBadRequest:
		if strings.Contains(msg, "not supported") {
			return clierrors.Newf("Database operations are not supported for this environment").
				WithSuggestion("Run 'metaplay database info ENVIRONMENT' to see which operations are available").
				WithCause(httpErr)
		}
		return clierrors.Newf("Invalid request while trying to %s: %s", operationDescription, msg).
			WithCause(httpErr)
	case http.StatusNotFound:
		return clierrors.Newf("Not found while trying to %s: %s", operationDescription, msg).
			WithCause(httpErr)
	case http.StatusConflict:
		hint := ""
		switch {
		case strings.Contains(msg, "quota exceeded"):
			hint = "Delete older manual snapshots before creating new ones"
		case strings.Contains(msg, "being created"), strings.Contains(msg, "backtracking"), strings.Contains(msg, "is in"):
			hint = "Wait for the in-progress operation to finish, then try again"
		}
		e := clierrors.Newf("Cannot %s: %s", operationDescription, msg).WithCause(httpErr)
		if hint != "" {
			e = e.WithSuggestion(hint)
		}
		return e
	case http.StatusUnauthorized, http.StatusForbidden:
		return clierrors.Newf("Not authorized to %s: %s", operationDescription, msg).
			WithSuggestion("Log in again with 'metaplay auth login'").
			WithCause(httpErr)
	default:
		if msg != "" {
			return clierrors.Newf("Failed to %s: %s", operationDescription, msg).WithCause(httpErr)
		}
		return clierrors.Wrapf(httpErr, "Failed to %s", operationDescription)
	}
}

// parseRollbackTargetTime parses the value of a --target-time flag. It accepts
// either an absolute RFC3339 timestamp (e.g. "2026-04-09T15:00:00Z") or a
// positive Go duration (e.g. "30m", "2h") meaning "that long ago from now".
// nowUTC is injected so tests can control the reference time.
func parseRollbackTargetTime(s string, nowUTC time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return time.Time{}, clierrors.NewUsageError("Missing --target-time value").
			WithSuggestion("Pass an absolute timestamp (2026-04-09T15:00:00Z) or a relative duration (30m, 2h)")
	}
	// Try absolute RFC3339 first.
	if ts, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return ts.UTC(), nil
	}
	// Try relative duration.
	if d, err := time.ParseDuration(trimmed); err == nil {
		if d < 0 {
			return time.Time{}, clierrors.NewUsageErrorf("Relative --target-time %q must be positive", trimmed).
				WithSuggestion("Use a positive duration like 30m or 2h to mean 'that long ago'")
		}
		if d == 0 {
			return time.Time{}, clierrors.NewUsageError("Relative --target-time must be greater than zero").
				WithSuggestion("Use a positive duration like 30m or 2h")
		}
		return nowUTC.Add(-d), nil
	}
	return time.Time{}, clierrors.NewUsageErrorf("Invalid --target-time value %q", trimmed).
		WithSuggestion("Use an absolute RFC3339 timestamp (2026-04-09T15:00:00Z) or a relative duration (30m, 2h)")
}

// resolveTargetShards picks the set of shard indices a mutating operation
// should target based on the env's capabilities and the --shard / --all-shards
// flags. In interactive mode, if the env has multiple shards and neither flag
// is set, the user is prompted to choose a shard.
//
// shardFlag == -1 means the flag was not set on the command line.
func resolveTargetShards(
	ctx context.Context,
	caps *envapi.DatabaseCapabilitiesResponse,
	shardFlag int,
	allShardsFlag bool,
) ([]int, error) {
	if len(caps.Shards) == 0 {
		return nil, clierrors.New("Database operations are not supported for this environment").
			WithSuggestion("Run 'metaplay database info ENVIRONMENT' to see which operations are available")
	}
	if shardFlag != -1 && allShardsFlag {
		return nil, clierrors.NewUsageError("Cannot use --shard and --all-shards together").
			WithSuggestion("Pick exactly one: either --shard=N or --all-shards")
	}

	// --all-shards: return every shard index from capabilities.
	if allShardsFlag {
		indices := make([]int, len(caps.Shards))
		for i, s := range caps.Shards {
			indices[i] = s.ShardIndex
		}
		return indices, nil
	}

	// --shard=N: validate the index is real.
	if shardFlag >= 0 {
		for _, s := range caps.Shards {
			if s.ShardIndex == shardFlag {
				return []int{shardFlag}, nil
			}
		}
		return nil, clierrors.NewUsageErrorf("Invalid --shard value %d", shardFlag).
			WithDetails(formatShardChoices(caps.Shards)).
			WithSuggestion("Pick one of the shard indices listed above")
	}

	// Single-shard env: default to the one shard, no prompt needed.
	if len(caps.Shards) == 1 {
		return []int{caps.Shards[0].ShardIndex}, nil
	}

	// Multi-shard env, neither flag set: prompt interactively or fail.
	if !tui.IsInteractiveMode() {
		return nil, clierrors.NewUsageError("Environment has multiple shards, --shard is required").
			WithDetails(formatShardChoices(caps.Shards)).
			WithSuggestion("Pass --shard=N to target a single shard, or --all-shards to target all of them")
	}
	picked, err := tui.ChooseFromListDialog(
		"Select target shard",
		caps.Shards,
		func(s *envapi.DatabaseShardCapabilities) (string, string) {
			return fmt.Sprintf("Shard %d", s.ShardIndex), s.ClusterID
		},
	)
	if err != nil {
		return nil, err
	}
	return []int{picked.ShardIndex}, nil
}

// formatShardChoices renders the available shards as a detail string for a
// usage error.
func formatShardChoices(shards []envapi.DatabaseShardCapabilities) string {
	parts := make([]string, len(shards))
	for i, s := range shards {
		parts[i] = fmt.Sprintf("%d (%s)", s.ShardIndex, s.ClusterID)
	}
	return "Available shards: " + strings.Join(parts, ", ")
}

// formatDatabaseAge renders the duration between t and now as a short human
// string (e.g. "2s", "1m15s", "3h02m", "2d", "3w", "5mo", "1y"). Used by
// `snapshot list` and `operation list` table columns.
func formatDatabaseAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%02ds", m, s)
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%02dm", h, m)
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// formatDatabaseTime renders a timestamp as the standard "YYYY-MM-DD HH:MM:SS
// UTC" format used by the database commands' text output.
func formatDatabaseTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05 MST")
}

// databaseOperationPollInterval returns the interval to use when polling
// a database operation for status updates. The interval is shorter in
// interactive mode (more responsive spinner feel) and longer in non-TTY
// mode to reduce log spam.
func databaseOperationPollInterval() time.Duration {
	if tui.IsInteractiveMode() {
		return 2 * time.Second
	}
	return 5 * time.Second
}

// printCapabilitiesUnsupported logs a friendly message explaining that the
// current environment does not support database operations.
func printCapabilitiesUnsupported(envName string) {
	log.Info().Msgf(
		"%s Database operations are not supported for environment '%s'",
		styles.RenderWarning("⚠"),
		styles.RenderTechnical(envName),
	)
	log.Info().Msg("  This environment does not have a dedicated, managed database cluster.")
}
