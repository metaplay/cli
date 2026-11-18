/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import "testing"

func TestFormatTraceDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{1, "00:00:01"},
		{30, "00:00:30"},
		{90, "00:01:30"},
		{3599, "00:59:59"},
		{3600, "01:00:00"}, // The maximum --duration. Was formatted as 00:60:00, which dotnet-trace rejects.
	}
	for _, tt := range tests {
		if got := formatTraceDuration(tt.seconds); got != tt.want {
			t.Errorf("formatTraceDuration(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}
