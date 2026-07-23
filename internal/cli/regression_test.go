package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runCLIErr captures stderr separately so warnings can be asserted.
func runCLIErr(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCommand()
	var out, errBuf strings.Builder
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errBuf.String(), err
}

func TestQRejectsUnknownFormat(t *testing.T) {
	_, err := runCLI(t, "q", "hello", "--api-key", "ck_test", "--format", "xml")
	if err == nil || !strings.Contains(err.Error(), "unknown --format") {
		t.Fatalf("q must validate --format like every other command, got %v", err)
	}
	if _, ok := err.(usageError); !ok {
		t.Errorf("want usageError (exit 2), got %T", err)
	}
}

func TestQHonorsExplicitFormat(t *testing.T) {
	srv := newChatServer(t, "hi")
	out, err := runCLI(t, "q", "hello",
		"--base-url", srv.URL, "--api-key", "ck_test",
		"--no-stream", "--format", "json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, `{"id":"chatcmpl-1"`) {
		t.Errorf("--format json ignored by q: out = %q", out)
	}
}

func TestAPIGetRejectsData(t *testing.T) {
	_, err := runCLI(t, "api", "get", "/models", "--api-key", "ck_test", "--data", `{"a":1}`)
	if err == nil || !strings.Contains(err.Error(), "not valid for a GET") {
		t.Fatalf("want usage error for --data on GET, got %v", err)
	}
}

func TestAPIGetSendsNoBody(t *testing.T) {
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	if _, err := runCLI(t, "api", "get", "/models",
		"--base-url", srv.URL, "--api-key", "ck_test"); err != nil {
		t.Fatal(err)
	}
	if gotLen > 0 {
		t.Errorf("GET carried a %d-byte body", gotLen)
	}
}

func TestModelPathMismatchWarns(t *testing.T) {
	srv := newChatServer(t, "hi")
	_, stderr, err := runCLIErr(t, "q", "hello",
		"--base-url", srv.URL+"/celeris-1", "--api-key", "ck_test",
		"--no-stream", "-m", "celeris-2",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "celeris-2") || !strings.Contains(stderr, "warning") {
		t.Errorf("want model/path mismatch warning, stderr = %q", stderr)
	}
}

func TestMatchingModelPathDoesNotWarn(t *testing.T) {
	srv := newChatServer(t, "hi")
	_, stderr, err := runCLIErr(t, "q", "hello",
		"--base-url", srv.URL+"/celeris-1", "--api-key", "ck_test",
		"--no-stream", "-m", "celeris-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("matching model must not warn, stderr = %q", stderr)
	}
}

func TestBareHostBaseURLDoesNotWarn(t *testing.T) {
	srv := newChatServer(t, "hi")
	_, stderr, err := runCLIErr(t, "q", "hello",
		"--base-url", srv.URL, "--api-key", "ck_test", "--no-stream", "-m", "celeris-9",
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("a base URL with no model segment must not warn, stderr = %q", stderr)
	}
}

// chatEcho answers like a chat completion and hands the request body back
// through the returned pointer, so tests can assert on what was sent.
func chatEcho(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		sent = b
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &sent
}

func sentMaxTokens(t *testing.T, body []byte) (int, bool) {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request %q: %v", body, err)
	}
	v, ok := req["max_tokens"]
	if !ok {
		return 0, false
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("max_tokens is %T, want a number", v)
	}
	return int(n), true
}

func TestDefaultMaxTokensIsSent(t *testing.T) {
	// q streams by default and needs --no-stream; the resource commands only
	// stream when asked.
	for _, args := range [][]string{
		{"q", "hello", "--no-stream"},
		{"chat:completions", "create", "-i", "hello"},
		{"completions", "create", "-p", "hello"},
	} {
		srv, body := chatEcho(t)
		full := append(args, "--base-url", srv.URL, "--api-key", "ck_test")
		if _, err := runCLI(t, full...); err != nil {
			t.Fatalf("%v: %v", args[0], err)
		}
		got, ok := sentMaxTokens(t, *body)
		if !ok || got != defaultMaxTokens {
			t.Errorf("%v sent max_tokens=%d (present=%v), want %d", args[0], got, ok, defaultMaxTokens)
		}
	}
}

func TestMaxTokensZeroOmitsTheField(t *testing.T) {
	srv, body := chatEcho(t)
	if _, err := runCLI(t, "q", "hello",
		"--base-url", srv.URL, "--api-key", "ck_test", "--no-stream",
		"--max-tokens", "0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := sentMaxTokens(t, *body); ok {
		t.Errorf("--max-tokens 0 must omit the field, sent %q", *body)
	}
}

func TestMaxTokensAcceptsAnyValueUpToTheLimit(t *testing.T) {
	srv, body := chatEcho(t)
	if _, err := runCLI(t, "q", "hello",
		"--base-url", srv.URL, "--api-key", "ck_test", "--no-stream",
		"--max-tokens", "8192"); err != nil {
		t.Fatal(err)
	}
	if got, _ := sentMaxTokens(t, *body); got != maxTokensLimit {
		t.Errorf("sent max_tokens=%d, want %d", got, maxTokensLimit)
	}
}

func TestCompletionCommandIsDisambiguated(t *testing.T) {
	root := NewRootCommand()
	c, _, err := root.Find([]string{"completion"})
	if err != nil || c == nil {
		t.Fatalf("completion command missing: %v", err)
	}
	if !strings.Contains(c.Short, "not the completions API") {
		t.Errorf("completion Short should disambiguate from `completions`: %q", c.Short)
	}
}
