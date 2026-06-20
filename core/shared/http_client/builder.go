// Package http_client provides a configurable HTTP client builder for
// outbound requests to external services.
package http_client

import (
	"fmt"
	"io"
	"net/http"
	"milton_prism/pkg/config"
)

type HTTPClient struct {
	client  *http.Client
	baseURL string
	enabled bool
}

func NewHTTPClient(cfg *config.HTTPClientCfg) *HTTPClient {
	return &HTTPClient{
		client:  &http.Client{Timeout: cfg.Timeout},
		baseURL: cfg.Host,
		enabled: *cfg.Enabled,
	}
}

func (c *HTTPClient) IsEnable() bool {
	return c.enabled
}

// Get sends a GET request to the specified URI.
func (c *HTTPClient) Get(uri string) ([]byte, error) {
	if !c.enabled {
		return nil, fmt.Errorf("http client is disabled")
	}

	url := c.baseURL + uri

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to send request GET: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	return body, nil
}

// Post sends a POST request to the specified URI with the given content type and data.
func (c *HTTPClient) Post(uri string, contentType string, data io.Reader) ([]byte, error) {
	if !c.enabled {
		return nil, fmt.Errorf("http client is disabled")
	}
	url := c.baseURL + uri

	resp, err := c.client.Post(url, contentType, data)
	if err != nil {
		return nil, fmt.Errorf("failed to send request POST: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	return body, nil
}

// Put sends a PUT request to the specified URI with the given content type and data.
func (c *HTTPClient) Put(uri string, contentType string, data io.Reader) ([]byte, error) {
	if !c.enabled {
		return nil, fmt.Errorf("http client is disabled")
	}

	url := c.baseURL + uri

	req, err := http.NewRequest(http.MethodPut, url, data)
	if err != nil {
		return nil, fmt.Errorf("failed to create request PUT: %v", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request PUT: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	return body, nil
}
