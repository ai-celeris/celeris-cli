package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ai-celeris/celeris-cli/internal/version"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                                    DefaultBaseURL + "/v1",
		"https://example.com/celeris-1":       "https://example.com/celeris-1/v1",
		"https://example.com/celeris-1/":      "https://example.com/celeris-1/v1",
		"https://example.com/celeris-1/v1":    "https://example.com/celeris-1/v1",
		"https://example.com/celeris-1/v1/":   "https://example.com/celeris-1/v1",
		" https://example.com/celeris-1/v1 ":  "https://example.com/celeris-1/v1",
		"https://example.com/celeris-1/v1///": "https://example.com/celeris-1/v1",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUserAgentFormat(t *testing.T) {
	ua := version.UserAgent()
	re := regexp.MustCompile(`^celeris-cli/[^ ]+ \([a-z0-9]+; [a-z0-9]+\) go/\d+\.\d+`)
	if !re.MatchString(ua) {
		t.Errorf("User-Agent %q does not match expected shape", ua)
	}
}

func TestChatCompletionSendsAuthAndUserAgent(t *testing.T) {
	var gotAuth, gotUA, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		gotBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(gotBody)
		fmt.Fprint(w, `{"id":"chatcmpl-1","model":"celeris-1","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	body, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:     "celeris-1",
		Messages:  []ChatMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer ck_test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotUA, "celeris-cli/") {
		t.Errorf("User-Agent = %q, want celeris-cli/ prefix", gotUA)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	var req ChatCompletionRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if req.MaxTokens != 256 || req.Model != "celeris-1" {
		t.Errorf("request = %+v", req)
	}
	if req.Temperature != nil {
		t.Errorf("unset temperature was sent: %v", *req.Temperature)
	}
	var resp ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "hi" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
}

func TestMissingAPIKey(t *testing.T) {
	c := New("https://example.invalid", "", 0, nil)
	_, err := c.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "CELERIS_API_KEY") {
		t.Errorf("want missing-key error, got %v", err)
	}
}

func TestAPIErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down","type":"rate_limit","code":"rate_limit_exceeded"}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	_, err := c.Models(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 429 || apiErr.Code != "rate_limit_exceeded" || apiErr.RetryAfter != "7" {
		t.Errorf("apiErr = %+v", apiErr)
	}
	if !strings.Contains(apiErr.Error(), "slow down") {
		t.Errorf("Error() = %q", apiErr.Error())
	}
}

func TestNonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "upstream exploded")
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	_, err := c.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("want body in error, got %v", err)
	}
}

func TestChatCompletionStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		fmt.Fprint(w, ": keep-alive comment\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	var got strings.Builder
	err := c.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model:    "celeris-1",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(chunk []byte) error {
		got.WriteString(DeltaText(chunk))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Hello" {
		t.Errorf("streamed text = %q, want Hello", got.String())
	}
}

func TestStreamWithoutDoneFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	err := c.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model:    "celeris-1",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func([]byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "[DONE]") {
		t.Errorf("want missing-DONE error, got %v", err)
	}
}

func TestStreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key","type":"auth","code":"invalid_api_key"}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_bad", 0, nil)
	err := c.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model:    "celeris-1",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func([]byte) error { return nil })
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "invalid_api_key" {
		t.Errorf("want invalid_api_key APIError, got %v", err)
	}
}

func TestLegacyStreamDeltaText(t *testing.T) {
	chunk := []byte(`{"id":"cmpl-1","object":"text_completion","choices":[{"text":"ok","index":0}]}`)
	if got := DeltaText(chunk); got != "ok" {
		t.Errorf("DeltaText = %q, want ok", got)
	}
}

func TestRawRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	if _, err := c.Raw(context.Background(), "get", "models", nil); err != nil {
		t.Fatal(err)
	}
}
