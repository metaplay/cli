/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHelmExtraArgs_SetBasic(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set", "key=value"})
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestParseHelmExtraArgs_SetStringBasic(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set-string", "key=true"})
	require.NoError(t, err)
	// --set-string forces string type, not boolean
	assert.Equal(t, "true", result["key"])
}

func TestParseHelmExtraArgs_SetTypeCoercion(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set", "count=42"})
	require.NoError(t, err)
	// --set coerces numeric values
	assert.Equal(t, int64(42), result["count"])
}

func TestParseHelmExtraArgs_NestedKey(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set", "a.b.c=val"})
	require.NoError(t, err)
	a, ok := result["a"].(map[string]any)
	require.True(t, ok)
	b, ok := a["b"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "val", b["c"])
}

func TestParseHelmExtraArgs_MultipleFlags(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{
		"--set", "foo=bar",
		"--set-string", "config.infraMigration=true",
		"--set", "replicas=3",
	})
	require.NoError(t, err)
	assert.Equal(t, "bar", result["foo"])
	assert.Equal(t, int64(3), result["replicas"])
	config, ok := result["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "true", config["infraMigration"])
}

func TestParseHelmExtraArgs_LaterOverridesEarlier(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{
		"--set", "key=first",
		"--set", "key=second",
	})
	require.NoError(t, err)
	assert.Equal(t, "second", result["key"])
}

func TestParseHelmExtraArgs_EqualsForm(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set=key=value"})
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestParseHelmExtraArgs_SetStringEqualsForm(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{"--set-string=key=42"})
	require.NoError(t, err)
	assert.Equal(t, "42", result["key"])
}

func TestParseHelmExtraArgs_UnknownFlag(t *testing.T) {
	_, err := ParseHelmExtraArgs([]string{"--values", "file.yaml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unrecognized Helm flag")
}

func TestParseHelmExtraArgs_SetMissingValue(t *testing.T) {
	_, err := ParseHelmExtraArgs([]string{"--set"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--set requires a value")
}

func TestParseHelmExtraArgs_SetStringMissingValue(t *testing.T) {
	_, err := ParseHelmExtraArgs([]string{"--set-string"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--set-string requires a value")
}

func TestParseHelmExtraArgs_EmptyArgs(t *testing.T) {
	result, err := ParseHelmExtraArgs([]string{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestParseHelmExtraArgs_NilArgs(t *testing.T) {
	result, err := ParseHelmExtraArgs(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}
