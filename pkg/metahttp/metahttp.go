/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metahttp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-resty/resty/v2"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
)

// Wrapper object for accessing an environment within a target stack.
type Client struct {
	TokenSet *auth.TokenSet // Tokens to use to access the environment.
	BaseURL  string         // Base URL of the target API (e.g. 'https://api.metaplay.io')
	Resty    *resty.Client  // Resty client with authorization header configured.
}

// NewJsonClient creates a new HTTP client with the given auth token set and base URL.
func NewJsonClient(tokenSet *auth.TokenSet, baseURL string) *Client {
	restyClient := resty.New().
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

// Make a HTTP request to the target URL with the specified method and body, and unmarshal the response into the specified type.
func Request[TResponse any](c *Client, method string, url string, body any) (TResponse, error) {
	var result TResponse

	// Perform the request
	var response *resty.Response
	var err error
	switch method {
	case http.MethodGet:
		response, err = c.Resty.R().Get(url)
	case http.MethodPost:
		response, err = c.Resty.R().SetBody(body).Post(url)
	case http.MethodPut:
		response, err = c.Resty.R().SetBody(body).Put(url)
	case http.MethodDelete:
		if body != nil {
			response, err = c.Resty.R().SetBody(body).Delete(url)
		} else {
			response, err = c.Resty.R().Delete(url)
		}
	default:
		log.Panic().Msgf("HTTP request method '%s' not implemented", method)
	}

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
	return Request[TResponse](c, http.MethodGet, url, nil)
}

// Make a HTTP POST to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Post[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodPost, url, body)
}

// Make a HTTP PUT to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Put[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodPut, url, body)
}

// Make a HTTP DELETE to the target URL with the specified body and unmarshal the response into the specified type.
// URL should start with a slash, e.g. "/v0/credentials/123/k8s"
func Delete[TResponse any](c *Client, url string, body any) (TResponse, error) {
	return Request[TResponse](c, http.MethodDelete, url, body)
}
