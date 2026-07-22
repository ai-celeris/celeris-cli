// Package api is a minimal typed client for the Celeris inference API
// (OpenAI wire format). It is hand-rolled rather than wrapping an SDK so the
// binary stays small and the User-Agent is fully controlled by the CLI.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-celeris/celeris-cli/internal/version"
)

// DefaultBaseURL targets the production Celeris-1 endpoint.
const DefaultBaseURL = "https://inference.cloud.celeris.ai/celeris-1"

// Client issues authenticated requests against one Celeris endpoint.
type Client struct {
	baseURL string // normalized, ends with /v1
	apiKey  string
	http    *http.Client
	debug   io.Writer // nil disables request/response tracing
}

// NormalizeBaseURL trims trailing slashes and appends /v1 unless the URL
// already ends with it. CELERIS_BASE_URL is documented as the endpoint root
// without /v1, but users paste both forms.
func NormalizeBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		u = DefaultBaseURL
	}
	if !strings.HasSuffix(u, "/v1") {
		u += "/v1"
	}
	return u
}

// New builds a client. A zero timeout means no client-side deadline, which
// streaming sessions rely on.
func New(baseURL, apiKey string, timeout time.Duration, debug io.Writer) *Client {
	return &Client{
		baseURL: NormalizeBaseURL(baseURL),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
		debug:   debug,
	}
}

// BaseURL reports the normalized endpoint, for --debug output.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("no API key: set CELERIS_API_KEY or pass --api-key")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "> %s %s\n> User-Agent: %s\n", method, req.URL, version.UserAgent())
		if body != nil {
			fmt.Fprintf(c.debug, "> %s\n", body)
		}
	}
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "< HTTP %d (%d bytes)\n", resp.StatusCode, len(data))
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp, data)
	}
	return data, nil
}

// Raw issues an arbitrary authenticated request under the /v1 base and
// returns the response body. It backs the `celeris api` escape hatch.
func (c *Client) Raw(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.do(ctx, strings.ToUpper(method), path, body)
}

// ChatCompletion issues a non-streaming chat completion and returns the raw
// response body.
func (c *Client) ChatCompletion(ctx context.Context, r ChatCompletionRequest) ([]byte, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/chat/completions", body)
}

// Completion issues a non-streaming legacy completion and returns the raw
// response body.
func (c *Client) Completion(ctx context.Context, r CompletionRequest) ([]byte, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/completions", body)
}

// Models lists the models the endpoint serves.
func (c *Client) Models(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/models", nil)
}
