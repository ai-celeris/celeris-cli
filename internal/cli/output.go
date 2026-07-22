package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ai-celeris/celeris-cli/internal/api"
)

const formatValues = "auto, text, json, jsonl, pretty, raw"

func checkFormat(format string) error {
	switch format {
	case "auto", "text", "json", "jsonl", "pretty", "raw":
		return nil
	}
	return usageErrorf("unknown --format %q (expected one of: %s)", format, formatValues)
}

// effectiveFormat resolves "auto": pretty JSON for terminals, bare text for
// pipelines.
func effectiveFormat(format string) string {
	if format != "auto" {
		return format
	}
	if stdoutIsTTY() {
		return "pretty"
	}
	return "text"
}

func ensureTrailingNewline(w io.Writer, s string) {
	fmt.Fprint(w, s)
	if s == "" || s[len(s)-1] != '\n' {
		fmt.Fprintln(w)
	}
}

func writeJSON(w io.Writer, body []byte, pretty bool) error {
	if pretty {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err == nil {
			body = buf.Bytes()
		}
	} else {
		var buf bytes.Buffer
		if err := json.Compact(&buf, body); err == nil {
			body = buf.Bytes()
		}
	}
	ensureTrailingNewline(w, string(body))
	return nil
}

// renderBody writes a non-streaming response. extractText pulls the
// human-readable content for text mode.
func renderBody(w io.Writer, format string, body []byte, extractText func([]byte) (string, error)) error {
	switch effectiveFormat(format) {
	case "text":
		s, err := extractText(body)
		if err != nil {
			return err
		}
		ensureTrailingNewline(w, s)
		return nil
	case "raw":
		ensureTrailingNewline(w, string(body))
		return nil
	case "pretty":
		return writeJSON(w, body, true)
	default: // json, jsonl
		return writeJSON(w, body, false)
	}
}

func chatText(body []byte) (string, error) {
	var r api.ChatCompletionResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("response contained no choices")
	}
	return r.Choices[0].Message.Content, nil
}

func completionText(body []byte) (string, error) {
	var r api.CompletionResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("response contained no choices")
	}
	return r.Choices[0].Text, nil
}

// streamRenderer returns the per-chunk handler for a streaming call plus a
// finisher that terminates the output cleanly.
func streamRenderer(w io.Writer, format string) (api.StreamHandler, func()) {
	if f := effectiveFormat(format); f == "json" || f == "jsonl" || f == "raw" || f == "pretty" {
		return func(chunk []byte) error {
			ensureTrailingNewline(w, string(chunk))
			flush(w)
			return nil
		}, func() {}
	}
	wroteAny := false
	handler := func(chunk []byte) error {
		if s := api.DeltaText(chunk); s != "" {
			wroteAny = true
			fmt.Fprint(w, s)
			flush(w)
		}
		return nil
	}
	return handler, func() {
		if wroteAny {
			fmt.Fprintln(w)
		}
	}
}

// flush pushes buffered output downstream immediately so streamed tokens
// appear as they arrive even when stdout is a pipe.
func flush(w io.Writer) {
	type flusher interface{ Flush() error }
	if f, ok := w.(flusher); ok {
		_ = f.Flush()
	}
	type syncer interface{ Sync() error }
	if s, ok := w.(syncer); ok {
		_ = s.Sync()
	}
}
