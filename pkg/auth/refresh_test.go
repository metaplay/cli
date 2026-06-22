/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import "testing"

func TestMergeRefreshedTokenSet(t *testing.T) {
	previous := &TokenSet{
		IDToken:      "old-id-token",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}

	tests := []struct {
		name             string
		refreshed        *TokenSet
		wantIDToken      string
		wantAccessToken  string
		wantRefreshToken string
	}{
		{
			name:             "server returns all fields",
			refreshed:        &TokenSet{IDToken: "new-id-token", AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
			wantIDToken:      "new-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "new-refresh-token",
		},
		{
			name:             "server omits id_token (Ory behavior)",
			refreshed:        &TokenSet{IDToken: "", AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
			wantIDToken:      "old-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "new-refresh-token",
		},
		{
			name:             "server omits refresh_token (rotation disabled)",
			refreshed:        &TokenSet{IDToken: "new-id-token", AccessToken: "new-access-token", RefreshToken: ""},
			wantIDToken:      "new-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "old-refresh-token",
		},
		{
			name:             "server returns only a new access_token",
			refreshed:        &TokenSet{IDToken: "", AccessToken: "new-access-token", RefreshToken: ""},
			wantIDToken:      "old-id-token",
			wantAccessToken:  "new-access-token",
			wantRefreshToken: "old-refresh-token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRefreshedTokenSet(previous, tc.refreshed)
			if got.IDToken != tc.wantIDToken {
				t.Errorf("IDToken = %q, want %q", got.IDToken, tc.wantIDToken)
			}
			if got.AccessToken != tc.wantAccessToken {
				t.Errorf("AccessToken = %q, want %q", got.AccessToken, tc.wantAccessToken)
			}
			if got.RefreshToken != tc.wantRefreshToken {
				t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, tc.wantRefreshToken)
			}
		})
	}
}
