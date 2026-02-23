/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metahttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/go-resty/resty/v2"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/httputil"
	"github.com/rs/zerolog/log"
)

// Wrapper object for accessing an environment within a target stack.
type Client struct {
	TokenSet *auth.TokenSet // Tokens to use to access the environment.
	BaseURL  string         // Base URL of the target API (e.g. 'https://api.metaplay.io')
	Resty    *resty.Client  // Resty client with authorization header configured.
}

// NewJSONClient creates a new HTTP client with the given auth token set and base URL.
// All failed requests are automatically retried a few times to mitigate network errors.
func NewJSONClient(tokenSet *auth.TokenSet, baseURL string) *Client {
	restyClient := httputil.NewRetryClient().
		SetAuthToken(tokenSet.AccessToken).
		SetBaseURL(baseURL).
		SetHeader("accept", "application/json").
		SetHeader("X-Application-Name", fmt.Sprintf("MetaplayCLI/%s", version.AppVersion))
	return &Client{
		TokenSet: tokenSet,
		BaseURL:  baseURL,
		Resty:    restyClient,
	}
}

// Download a file from the specified URL to the specified file path.
// Note: The file gets created even if the request fails.
func Download(c *Client, url string, filePath string) (*resty.Response, error) {
	// Perform the request: download directly to a file.
	response, err := c.Resty.R().SetOutput(filePath).Get(url)

	if err != nil {
		return nil, fmt.Errorf("Failed to download file from %s%s: %w", c.BaseURL, filePath, err)
	}

	return response, nil
}

// DownloadWithProgress downloads a file from the specified URL to the specified
// file path, calling onProgress periodically with the number of bytes downloaded
// and the total size (0 if unknown).
func DownloadWithProgress(c *Client, url string, filePath string, onProgress func(downloaded, total int64)) (*resty.Response, error) {
	// Use SetDoNotParseResponse to get raw response body for streaming.
	resp, err := c.Resty.R().SetDoNotParseResponse(true).Get(url)
	if err != nil {
		return nil, fmt.Errorf("Failed to download file from %s%s: %w", c.BaseURL, url, err)
	}

	rawBody := resp.RawBody()
	defer rawBody.Close()

	// On HTTP error, return the response without a Go error so the caller can
	// inspect resp.StatusCode() and produce a domain-specific error message.
	if resp.IsError() {
		return resp, nil
	}

	// Create the output file.
	outFile, err := os.Create(filePath)
	if err != nil {
		return resp, fmt.Errorf("Failed to create output file %s: %w", filePath, err)
	}

	// Determine total size from Content-Length header.
	var total int64
	if cl := resp.Header().Get("Content-Length"); cl != "" {
		total, _ = strconv.ParseInt(cl, 10, 64)
	}

	// Stream response body to file with progress tracking.
	pr := &progressReader{
		reader:     rawBody,
		total:      total,
		onProgress: onProgress,
	}

	_, copyErr := io.Copy(outFile, pr)
	outFile.Close()
	if copyErr != nil {
		os.Remove(filePath)
		return resp, fmt.Errorf("Failed to write downloaded file %s: %w", filePath, copyErr)
	}

	return resp, nil
}

// progressReader wraps an io.Reader and reports progress via a callback.
type progressReader struct {
	reader     io.Reader
	downloaded int64
	total      int64
	onProgress func(downloaded, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.downloaded += int64(n)
	if pr.onProgress != nil {
		pr.onProgress(pr.downloaded, pr.total)
	}
	return n, err
}

// Make a HTTP request to the target URL with the specified method and body, and unmarshal the response into the specified type.
func Request[TResponse any](c *Client, method string, url string, body any, contentType string) (TResponse, error) {
	var result TResponse

	// Perform the request
	var response *resty.Response
	var err error
	request := c.Resty.R()

	if contentType != "" {
		request.SetHeader("Content-Type", contentType)
	}

	switch method {
	case http.MethodGet:
		response, err = request.Get(url)
	case http.MethodPost:
		response, err = request.SetBody(body).Post(url)
	case http.MethodPut:
		response, err = request.SetBody(body).Put(url)
	case http.MethodDelete:
		if body != nil {
			response, err = request.SetBody(body).Delete(url)
		} else {
			response, err = request.Delete(url)
		}
	default:
		log.Panic().Msgf("HTTP request method '%s' not implemented", method)
	}

	log.Debug().Msgf("Raw request: %+v", response.Request.RawRequest)

	// Handle request errors
	if err != nil {
		return result, fmt.Errorf("%s request to %s%s failed: %w", method, c.BaseURL, url, err)
	}

	// Debug log the raw response.
	// log.Info().Msgf("Raw response from %s: %s", url, string(response.Body()))

	// Check response status code
	if response.StatusCode() < http.StatusOK || response.StatusCode() >= http.StatusMultipleChoices {
		// Print error details before return the error to keep the log more readable.
		errorBody := string(response.Body())
		requestURL := fmt.Sprintf("%s%s", c.BaseURL, url)
		log.Error().Msgf("Request failed with status code %d (%s %s): %s", response.StatusCode(), method, requestURL, errorBody)
		return result, fmt.Errorf("%s %s failed (see above for details)", method, requestURL)
	}

	// If type TResult is just string, get the body of the HTTP response as plaintext
	if _, isReturnTypeString := any(result).(string); isReturnTypeString {
		result = any(response.String()).(TResponse)
	} else {
		// For complex types, get the body as JSON and unmarshal into TResult.
		rawBody := response.Body()
		err = json.Unmarshal(rawBody, &result)
		if err != nil {
			log.Error().Msgf("Failed to unmarshal response: %v, raw body: %s", err, rawBody)
			return result, err
		}

	}

	return result, nil
}

// Make a HTTP GET to the target URL and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Get[TResponse any](c *Client, url string) (TResponse, error) {
	return Request[TResponse](c, http.MethodGet, url, nil, "")
}

// Make a HTTP POST to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Post[TResponse any](c *Client, url string, body any, contentType string) (TResponse, error) {
	return Request[TResponse](c, http.MethodPost, url, body, contentType)
}

// Make a HTTP PUT to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Put[TResponse any](c *Client, url string, body any, contentType string) (TResponse, error) {
	return Request[TResponse](c, http.MethodPut, url, body, contentType)
}

// Make a HTTP DELETE to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Delete[TResponse any](c *Client, url string, body any, contentType string) (TResponse, error) {
	return Request[TResponse](c, http.MethodDelete, url, body, contentType)
}

// Make a HTTP POST to the target URL with the specified body (with JSON as mimetype) and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func PostJSON[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodPost, url, body, "application/json")
}

// Make a HTTP PUT to the target URL with the specified body (with JSON as mimetype) and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func PutJSON[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodPut, url, body, "application/json")
}

// Make a HTTP DELETE to the target URL with the specified body (with JSON as mimetype) and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func DeleteJSON[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodDelete, url, body, "application/json")
}
