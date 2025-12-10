/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package httputil

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// isRetryableError checks if an error or status code should trigger a retry.
func isRetryableError(resp *resty.Response, err error) bool {
	if err != nil {
		return true // Network errors are generally transient
	}
	if resp == nil {
		return true
	}
	// Retry on server errors and rate limiting
	statusCode := resp.StatusCode()
	return statusCode == 429 || statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
}

// NewRetryClient creates a resty client with retry logic but no authentication.
func NewRetryClient() *resty.Client {
	return resty.New().
		SetRetryCount(3).
		SetRetryWaitTime(1 * time.Second).
		SetRetryMaxWaitTime(8 * time.Second).
		AddRetryCondition(isRetryableError).
		AddRetryHook(func(resp *resty.Response, err error) {
			if err != nil {
				log.Warn().Msgf("Request failed with error, retrying: %v", err)
			} else if resp != nil {
				log.Warn().Msgf("Request failed with status %d, retrying...", resp.StatusCode())
			}
		})
}

// GetBytesWithRetry performs an HTTP GET to the specified URL without authentication.
// Returns the response body as bytes. Includes retry logic for transient errors.
func GetBytesWithRetry(url string) ([]byte, error) {
	client := NewRetryClient()
	resp, err := client.R().Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET request to %s failed: %w", url, err)
	}
	if resp.StatusCode() < http.StatusOK || resp.StatusCode() >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s failed with status %d: %s", url, resp.StatusCode(), resp.String())
	}
	return resp.Body(), nil
}

// PostFormWithRetry performs a form-encoded POST to the specified URL without authentication.
// Returns the response body as bytes and the HTTP status code. Includes retry logic for transient errors.
func PostFormWithRetry(url string, formData string) ([]byte, int, error) {
	client := NewRetryClient()
	resp, err := client.R().
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetBody(formData).
		Post(url)
	if err != nil {
		return nil, 0, fmt.Errorf("POST request to %s failed: %w", url, err)
	}
	return resp.Body(), resp.StatusCode(), nil
}
