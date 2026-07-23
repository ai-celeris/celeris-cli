// Package api is a minimal typed client for the Celeris inference API
// (OpenAI wire format). It is hand-rolled rather than wrapping an SDK so the
// binary stays small and the User-Agent is fully controlled by the CLI.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ai-celeris/celeris-cli/internal/version"
)

// DefaultHost is the production Celeris inference host. Production endpoints
// embed the model id as a path segment (https://host/<model>/v1), so the
// endpoint a request goes to depends on the model it selects.
const DefaultHost = "https://inference.celeris.ai"

// DefaultModel is the model served when nothing overrides it.
const DefaultModel = "celeris-1"

// DefaultBaseURL is the production endpoint for DefaultModel.
const DefaultBaseURL = DefaultHost + "/" + DefaultModel

// DefaultBaseURLForModel builds the production endpoint that serves one model.
// Because the model id is a path segment, pointing at the wrong endpoint makes
// the service reject the request, so callers derive the URL from the model
// rather than assuming DefaultBaseURL.
func DefaultBaseURLForModel(model string) string {
	if model == "" {
		return DefaultBaseURL
	}
	return DefaultHost + "/" + model
}

// ModelPathSegment reports the model id embedded in a base URL's path, or ""
// when the URL carries no such segment (a bare host, or a proxy that does not
// use the production layout). The input may be raw or normalized.
func ModelPathSegment(baseURL string) string {
	u, err := url.Parse(NormalizeBaseURL(baseURL))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Need at least "<model>/v1"; a lone "v1" means no model segment.
	if len(parts) < 2 || parts[len(parts)-1] != "v1" {
		return ""
	}
	return parts[len(parts)-2]
}

// Client issues authenticated requests against one Celeris endpoint.
type Client struct {
	baseURL string // normalized, ends with /v1
	apiKey  string
	http    *http.Client
	debug   io.Writer // nil disables request/response tracing
	headers http.Header
	retries int // additional attempts after a retryable failure
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

// Connection-setup budgets. These bound the phases before the first byte of
// the body, which a streaming session does not need to be unbounded: only the
// body itself must be free to run long.
const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 60 * time.Second
)

// streamSafeTransport bounds connect, TLS, and time-to-first-header without
// capping how long a response body may stream. A client with Timeout: 0 and
// the stock transport hangs for minutes against an unreachable endpoint.
func streamSafeTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = tlsHandshakeTimeout
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return t
}

// New builds a client. A zero timeout means no overall deadline, which
// streaming sessions rely on; connection setup is bounded regardless.
func New(baseURL, apiKey string, timeout time.Duration, debug io.Writer) *Client {
	return &Client{
		baseURL: NormalizeBaseURL(baseURL),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout, Transport: streamSafeTransport()},
		debug:   debug,
	}
}

// WithHeaders returns c configured to add headers to every request. Custom
// values are applied after the client's defaults, so they can override them.
func (c *Client) WithHeaders(headers http.Header) *Client {
	c.headers = headers.Clone()
	return c
}

// WithRetries sets how many extra attempts a retryable failure (429, 5xx, or
// a transport error) earns. Streaming calls are never retried: tokens already
// written to stdout cannot be taken back.
func (c *Client) WithRetries(n int) *Client {
	if n > 0 {
		c.retries = n
	}
	return c
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
	for name, values := range c.headers {
		req.Header.Del(name)
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "> %s %s\n> User-Agent: %s\n", method, req.URL, version.UserAgent())
		if body != nil {
			fmt.Fprintf(c.debug, "> %s\n", body)
		}
	}
	return req, nil
}

// baseRetryDelay is the first backoff step; each further attempt doubles it.
// A Retry-After header always wins over the computed value.
const baseRetryDelay = 500 * time.Millisecond

// retryable reports whether a status code is worth another attempt. 429 and
// 5xx are transient; 4xx below 429 are the caller's fault and never are.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// retryDelay honors Retry-After when the server sends a sane value, and falls
// back to exponential backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	backoff := baseRetryDelay << attempt
	if resp == nil {
		return backoff
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 && secs <= 300 {
			return time.Duration(secs) * time.Second
		}
	}
	return backoff
}

// sleepCtx waits for d unless the context ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		data, resp, err := c.attempt(ctx, method, path, body)
		switch {
		case err == nil && !retryable(resp.StatusCode):
			if resp.StatusCode >= 400 {
				return nil, parseAPIError(resp, data)
			}
			return data, nil
		case err != nil:
			// A malformed request (missing key, bad URL) never becomes valid
			// on a second try; only transport failures are retryable.
			if resp == nil && !isTransportError(err) {
				return nil, err
			}
			lastErr = err
		default:
			lastErr = parseAPIError(resp, data)
		}
		if attempt >= c.retries {
			return nil, lastErr
		}
		delay := retryDelay(resp, attempt)
		if c.debug != nil {
			fmt.Fprintf(c.debug, "< retrying in %s (attempt %d/%d): %v\n",
				delay, attempt+1, c.retries, lastErr)
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, lastErr
		}
	}
}

// attempt performs one request round-trip. It returns the response alongside
// the body so callers can inspect the status without re-reading.
func (c *Client) attempt(ctx context.Context, method, path string, body []byte) ([]byte, *http.Response, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "< HTTP %d (%d bytes)\n", resp.StatusCode, len(data))
	}
	return data, resp, nil
}

// isTransportError distinguishes a failed round-trip (worth retrying) from a
// request the client refused to build (never worth retrying).
func isTransportError(err error) bool {
	var ue *url.Error
	return errors.As(err, &ue)
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
