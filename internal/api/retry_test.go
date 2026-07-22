package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRetriesRateLimitThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil).WithRetries(2)
	if _, err := c.Models(context.Background()); err != nil {
		t.Fatalf("want success after retry, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestRetriesExhaustedReturnsAPIError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil).WithRetries(1)
	_, err := c.Models(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 503 {
		t.Fatalf("want 503 APIError, got %T %v", err, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (1 initial + 1 retry)", got)
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"bad key","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_bad", 0, nil).WithRetries(3)
	_, err := c.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("want auth error, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (401 must not be retried)", got)
	}
}

func TestMissingKeyIsNotRetried(t *testing.T) {
	c := New("https://example.invalid", "", 0, nil).WithRetries(3)
	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("want missing-key error")
	}
	// A missing key can never become valid; the retry loop must not spin on it.
}

func TestDefaultBaseURLForModel(t *testing.T) {
	cases := map[string]string{
		"":          DefaultBaseURL,
		"celeris-1": DefaultHost + "/celeris-1",
		"celeris-2": DefaultHost + "/celeris-2",
	}
	for model, want := range cases {
		if got := DefaultBaseURLForModel(model); got != want {
			t.Errorf("DefaultBaseURLForModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestModelPathSegment(t *testing.T) {
	cases := map[string]string{
		"https://inference.cloud.celeris.ai/celeris-1":    "celeris-1",
		"https://inference.cloud.celeris.ai/celeris-1/v1": "celeris-1",
		"https://inference.cloud.celeris.ai/celeris-2/":   "celeris-2",
		"http://127.0.0.1:8791":                           "", // bare host: no model segment
		"http://127.0.0.1:8791/v1":                        "",
	}
	for in, want := range cases {
		if got := ModelPathSegment(in); got != want {
			t.Errorf("ModelPathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamWithoutDoneButFinishReasonSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}]}\n\n"))
	}))
	defer srv.Close()

	c := New(srv.URL, "ck_test", 0, nil)
	err := c.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model:    "celeris-1",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func([]byte) error { return nil })
	if err != nil {
		t.Errorf("finish_reason should stand in for [DONE], got %v", err)
	}
}
