// Package util holds shared, non-domain helpers (CONTRACT §5). Every module must
// call these instead of reimplementing HTTP, request construction, JSON handling,
// or error mapping.
package util

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout is the standard timeout for outbound HTTP calls.
const DefaultTimeout = 30 * time.Second

// HTTPClient is the unified HTTP client wrapper. Create it via NewHTTPClient;
// do not instantiate http.Client directly in modules.
type HTTPClient struct {
	client  *http.Client
	baseURL string
	headers map[string]string
}

// NewHTTPClient returns a unified HTTP client with the given timeout and optional
// base URL (empty for absolute URLs only).
func NewHTTPClient(timeout time.Duration, baseURL string) *HTTPClient {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &HTTPClient{
		client:  &http.Client{Timeout: timeout},
		baseURL: baseURL,
		headers: map[string]string{},
	}
}

// SetHeader sets a default header applied to every request.
func (c *HTTPClient) SetHeader(key, value string) {
	c.headers[key] = value
}

// NewRequest builds an http.Request with the unified conventions: base URL
// resolution, method, body (JSON-encoded when body != nil), and default headers.
func (c *HTTPClient) NewRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	url := path
	if c.baseURL != "" {
		url = c.baseURL + path
	}

	var reader io.Reader
	if body != nil {
		data, err := Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Do executes the request and decodes the JSON response into out (when non-nil).
// It returns the raw status code alongside any decode error.
func (c *HTTPClient) Do(req *http.Request, out any) (int, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && len(data) > 0 {
		if err := Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// Get is a convenience helper: GET path and decode into out.
func (c *HTTPClient) Get(ctx context.Context, path string, out any) (int, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, err
	}
	return c.Do(req, out)
}

// DoRaw executes the request and returns the status code and raw response body
// (without JSON decoding). Used by modules that need the raw text for error
// messages or custom JSON shapes (e.g. remail order/wallet responses).
func (c *HTTPClient) DoRaw(req *http.Request) (int, []byte, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}
