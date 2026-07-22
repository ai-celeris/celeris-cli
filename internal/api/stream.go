package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxSSELine bounds a single SSE line; completion chunks are tiny, so 1 MiB
// is generous headroom rather than a real limit.
const maxSSELine = 1 << 20

// StreamHandler receives each SSE data payload (one JSON chunk) as it
// arrives. Returning an error aborts the stream.
type StreamHandler func(chunk []byte) error

func (c *Client) stream(ctx context.Context, path string, body []byte, h StreamHandler) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxSSELine))
		return parseAPIError(resp, data)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), maxSSELine)
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // comments, event names, blank keep-alive lines
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			return nil
		}
		if err := h([]byte(data)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	return fmt.Errorf("stream ended without [DONE] terminator")
}

// ChatCompletionStream issues a streaming chat completion, invoking h per
// chunk.
func (c *Client) ChatCompletionStream(ctx context.Context, r ChatCompletionRequest, h StreamHandler) error {
	r.Stream = true
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return c.stream(ctx, "/chat/completions", body, h)
}

// CompletionStream issues a streaming legacy completion, invoking h per
// chunk.
func (c *Client) CompletionStream(ctx context.Context, r CompletionRequest, h StreamHandler) error {
	r.Stream = true
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return c.stream(ctx, "/completions", body, h)
}

// DeltaText extracts the incremental text from a stream chunk, handling both
// chat (delta.content) and legacy (text) shapes.
func DeltaText(chunk []byte) string {
	var sc StreamChunk
	if err := json.Unmarshal(chunk, &sc); err != nil || len(sc.Choices) == 0 {
		return ""
	}
	if sc.Choices[0].Delta.Content != "" {
		return sc.Choices[0].Delta.Content
	}
	return sc.Choices[0].Text
}
