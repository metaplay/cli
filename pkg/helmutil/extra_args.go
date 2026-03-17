/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	clierrors "github.com/metaplay/cli/internal/errors"
	"helm.sh/helm/v3/pkg/strvals"
)

// ParseHelmExtraArgs parses --set and --set-string flags from a raw args slice
// into a values map suitable for Helm. Later flags override earlier ones, matching
// Helm CLI behavior.
func ParseHelmExtraArgs(args []string) (map[string]any, error) {
	result := map[string]any{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--set":
			i++
			if i >= len(args) {
				return nil, clierrors.NewUsageError("--set requires a value (e.g., --set key=value)")
			}
			if err := strvals.ParseInto(args[i], result); err != nil {
				return nil, clierrors.Wrapf(err, "Failed to parse --set value '%s'", args[i])
			}

		case "--set-string":
			i++
			if i >= len(args) {
				return nil, clierrors.NewUsageError("--set-string requires a value (e.g., --set-string key=value)")
			}
			if err := strvals.ParseIntoString(args[i], result); err != nil {
				return nil, clierrors.Wrapf(err, "Failed to parse --set-string value '%s'", args[i])
			}

		default:
			// Check for --set=value and --set-string=value forms
			if val, ok := cutFlagValue(args[i], "--set="); ok {
				if err := strvals.ParseInto(val, result); err != nil {
					return nil, clierrors.Wrapf(err, "Failed to parse --set value '%s'", val)
				}
			} else if val, ok := cutFlagValue(args[i], "--set-string="); ok {
				if err := strvals.ParseIntoString(val, result); err != nil {
					return nil, clierrors.Wrapf(err, "Failed to parse --set-string value '%s'", val)
				}
			} else {
				return nil, clierrors.NewUsageErrorf("Unrecognized Helm flag '%s'", args[i]).
					WithSuggestion("Only --set and --set-string are supported as extra Helm arguments")
			}
		}
	}

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// cutFlagValue checks if s starts with the given prefix and returns the remainder.
func cutFlagValue(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}
