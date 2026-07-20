/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func TestIsRemoteImageNotFound(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "manifest unknown error code",
			err:  &transport.Error{Errors: []transport.Diagnostic{{Code: transport.ManifestUnknownErrorCode}}},
			want: true,
		},
		{
			name: "name unknown error code",
			err:  &transport.Error{Errors: []transport.Diagnostic{{Code: transport.NameUnknownErrorCode}}},
			want: true,
		},
		{
			name: "404 status without diagnostic code",
			err:  &transport.Error{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "wrapped not-found transport error",
			err:  fmt.Errorf("query failed: %w", &transport.Error{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "unauthorized is not a not-found",
			err:  &transport.Error{StatusCode: http.StatusUnauthorized, Errors: []transport.Diagnostic{{Code: transport.UnauthorizedErrorCode}}},
			want: false,
		},
		{
			name: "non-transport error",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRemoteImageNotFound(tc.err); got != tc.want {
				t.Errorf("isRemoteImageNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}
