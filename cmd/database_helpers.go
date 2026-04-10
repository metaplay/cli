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
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// resolveEnvironmentForDatabaseOps is the shared entry-point for every
// 'metaplay database ...' command. It resolves the target environment from
// the positional argument (via tryResolveProject + resolveEnvironment),
// builds a TargetEnvironment, and probes the database capabilities endpoint
// as a readiness check.
//
// The capabilities probe is the canonical "are database operations available
// for this environment?" signal:
//
//   - On infrastructure stacks that predate the database operations API,
//     the endpoint is not registered and the probe fails with a transport
//     or HTTP error; we translate this into a clear "not available" error.
//   - On stacks that support the API, the probe returns capabilities data
//     that callers use for subsequent shard selection and per-shard logic.
//
// Treating any capabilities error as "not available" is intentional: it
// avoids fragile inference about error-body shapes and gives consistent,
// actionable feedback across every database subcommand. The underlying
// error is attached via WithCause for troubleshooting.
func resolveEnvironmentForDatabaseOps(
	ctx context.Context,
	argEnvironment string,
) (*metaproj.ProjectEnvironmentConfig, *envapi.TargetEnvironment, *envapi.DatabaseCapabilitiesResponse, error) {
	project, err := tryResolveProject()
	if err != nil {
		return nil, nil, nil, err
	}
	envConfig, tokenSet, err := resolveEnvironment(ctx, project, argEnvironment)
	if err != nil {
		return nil, nil, nil, err
	}
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	caps, err := targetEnv.GetDatabaseCapabilities()
	if err != nil {
		return nil, nil, nil, clierrors.New("Database operations are not available for this environment").
			WithDetails(
				"Could not reach the database capabilities endpoint on the environment's",
				"infrastructure stack. This usually means the infrastructure stack is",
				"running an older version that does not include these features.",
			).
			WithSuggestion("Contact Metaplay Support or your infrastructure administrator if you expect this environment to support database operations").
			WithCause(err)
	}
	return envConfig, targetEnv, caps, nil
}

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

// ensureShardsSupportCapability checks that every shard in targetIndices
// reports the required capability as enabled in the capabilities response.
// Returns nil if all selected shards are good, or a clean CLIError listing
// the shards that do not support the operation otherwise. operationLabel is
// the subject of the error message, e.g. "Snapshot creation", "Rollback".
//
// This is a local pre-flight check that avoids round-tripping an invalid
// operation to the backend when the capabilities response already tells us
// it would be rejected.
func ensureShardsSupportCapability(
	caps *envapi.DatabaseCapabilitiesResponse,
	targetIndices []int,
	check func(envapi.DatabaseShardCapabilities) bool,
	operationLabel string,
) error {
	shardByIndex := make(map[int]envapi.DatabaseShardCapabilities, len(caps.Shards))
	for _, s := range caps.Shards {
		shardByIndex[s.ShardIndex] = s
	}
	var unsupported []string
	for _, idx := range targetIndices {
		shard, ok := shardByIndex[idx]
		if !ok {
			// Caller should have validated this via resolveTargetShards.
			continue
		}
		if !check(shard) {
			unsupported = append(unsupported, fmt.Sprintf("shard %d (%s)", idx, shard.ClusterID))
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	if len(targetIndices) == 1 {
		return clierrors.Newf("%s is not supported on %s", operationLabel, unsupported[0]).
			WithSuggestion("Run 'metaplay database info ENVIRONMENT' to see which operations are available on each shard")
	}
	return clierrors.Newf("%s is not supported on %d of %d target shards", operationLabel, len(unsupported), len(targetIndices)).
		WithDetails(unsupported...).
		WithSuggestion("Pick a different --shard, or run 'metaplay database info ENVIRONMENT' to see which operations are available")
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

// waitForDatabaseOperation polls the given operation id until it reaches a
// terminal state or the context is cancelled. The onStatusChange callback is
// invoked each time the operation's Status field changes. HTTPError responses
// are mapped to friendly CLI errors via mapDatabaseHTTPError.
func waitForDatabaseOperation(
	ctx context.Context,
	target *envapi.TargetEnvironment,
	operationID string,
	onStatusChange func(*envapi.DatabaseOperation),
) (*envapi.DatabaseOperation, error) {
	var lastStatus string
	pollInterval := databaseOperationPollInterval()
	for {
		op, err := target.GetDatabaseOperation(operationID)
		if err != nil {
			return nil, mapDatabaseHTTPError(err, "get operation")
		}
		if op.Status != lastStatus {
			if onStatusChange != nil {
				onStatusChange(op)
			}
			lastStatus = op.Status
		}
		if op.IsTerminal() {
			return op, nil
		}
		select {
		case <-ctx.Done():
			return nil, clierrors.Wrap(ctx.Err(), "Operation watch cancelled")
		case <-time.After(pollInterval):
		}
	}
}

// shardOperationResult captures the outcome of a mutating operation on a
// single shard, returned from per-shard parallel fan-out helpers.
type shardOperationResult struct {
	ShardIndex int
	Operation  *envapi.DatabaseOperation
	Err        error
}

// runShardOperation initiates a mutating database operation (via the
// provided initiate func) on one shard, logs the accepted operation id, and
// unless noWait is true, polls the operation until it reaches a terminal
// state. operationDescription is used in user-visible messages ("snapshot
// create", "snapshot delete", "rollback database"). multiShard controls
// whether per-shard log lines are prefixed with "[shard N]".
func runShardOperation(
	ctx context.Context,
	target *envapi.TargetEnvironment,
	shardIndex int,
	operationDescription string,
	initiate func() (*envapi.DatabaseOperation, error),
	noWait bool,
	multiShard bool,
) shardOperationResult {
	prefix := ""
	if multiShard {
		prefix = fmt.Sprintf("[shard %d] ", shardIndex)
	}

	op, err := initiate()
	if err != nil {
		return shardOperationResult{
			ShardIndex: shardIndex,
			Err:        mapDatabaseHTTPError(err, operationDescription),
		}
	}
	log.Info().Msgf("%s%s %s accepted (op: %s)",
		prefix,
		styles.RenderSuccess("✓"),
		operationDescription,
		styles.RenderTechnical(op.OperationID))

	if noWait || op.IsTerminal() {
		return shardOperationResult{ShardIndex: shardIndex, Operation: op}
	}

	final, err := waitForDatabaseOperation(ctx, target, op.OperationID,
		func(u *envapi.DatabaseOperation) {
			log.Info().Msgf("%s  status: %s", prefix, renderOperationStatusStyled(u.Status))
		})
	if err != nil {
		return shardOperationResult{ShardIndex: shardIndex, Operation: op, Err: err}
	}

	if final.Status == envapi.DatabaseOperationStatusFailed {
		msg := "operation failed"
		if final.Error != nil && *final.Error != "" {
			msg = *final.Error
		}
		return shardOperationResult{
			ShardIndex: shardIndex,
			Operation:  final,
			Err: clierrors.Newf("%s%s failed: %s",
				prefix, operationDescription, msg),
		}
	}

	log.Info().Msgf("%s%s %s completed", prefix, styles.RenderSuccess("✓"), operationDescription)
	return shardOperationResult{ShardIndex: shardIndex, Operation: final}
}

// aggregateShardResults joins multiple per-shard results into a single error
// summary. Returns nil if all shards succeeded, otherwise a CLIError listing
// the failed shards. Intended for --all-shards fan-out commands.
func aggregateShardResults(operationDescription string, results []shardOperationResult) error {
	var failed []string
	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, fmt.Sprintf("shard %d: %s", r.ShardIndex, r.Err.Error()))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return clierrors.Newf("%s failed on %d of %d shards", operationDescription, len(failed), len(results)).
		WithDetails(failed...)
}
