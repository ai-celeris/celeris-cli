package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the command tree with the given args and stdout capture.
// API-bound tests point --base-url at an httptest server.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func newChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"celeris-1","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatCreateTextFormat(t *testing.T) {
	srv := newChatServer(t, "Positive")
	out, err := runCLI(t,
		"chat:completions", "create",
		"--base-url", srv.URL, "--api-key", "ck_test",
		"--input", "great product", "--format", "text",
	)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Positive\n" {
		t.Errorf("out = %q", out)
	}
}

func TestChatCreateJSONFormat(t *testing.T) {
	srv := newChatServer(t, "hi")
	out, err := runCLI(t,
		"chat:completions", "create",
		"--base-url", srv.URL, "--api-key", "ck_test",
		"-i", "hello", "--format", "json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, `{"id":"chatcmpl-1"`) || strings.Count(out, "\n") != 1 {
		t.Errorf("out = %q", out)
	}
}

func TestChatCreateRequiresInput(t *testing.T) {
	_, err := runCLI(t, "chat:completions", "create", "--api-key", "ck_test")
	if err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Errorf("want no-messages usage error, got %v", err)
	}
	if _, ok := err.(usageError); !ok {
		t.Errorf("want usageError, got %T", err)
	}
}

func TestMaxTokensValidation(t *testing.T) {
	for _, n := range []string{"-1", "8193"} {
		_, err := runCLI(t,
			"chat:completions", "create",
			"--api-key", "ck_test", "-i", "hi", "--max-tokens", n,
		)
		if err == nil || !strings.Contains(err.Error(), "between 0 and 8192") {
			t.Errorf("--max-tokens %s: want usage error, got %v", n, err)
		}
	}
}

func TestMessageFlagParsing(t *testing.T) {
	if _, err := parseMessageFlag("nocolon"); err == nil {
		t.Error("want error for missing colon")
	}
	if _, err := parseMessageFlag("robot:hi"); err == nil {
		t.Error("want error for bad role")
	}
	m, err := parseMessageFlag("system:be brief: always")
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != "system" || m.Content != "be brief: always" {
		t.Errorf("m = %+v", m)
	}
}

func TestAtFileInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(path, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err == nil {
			gotPrompt = req.Prompt
		}
		fmt.Fprint(w, `{"id":"cmpl-1","object":"text_completion","model":"celeris-1","choices":[{"text":"ok","index":0,"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	out, err := runCLI(t,
		"completions", "create",
		"--base-url", srv.URL, "--api-key", "ck_test",
		"--prompt", "@"+path, "--format", "text",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "from file" {
		t.Errorf("prompt sent = %q", gotPrompt)
	}
	if out != "ok\n" {
		t.Errorf("out = %q", out)
	}
}

func TestModelsListTextFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[{"id":"celeris-1","object":"model","created":1751673600,"owned_by":"celeris"}]}`)
	}))
	defer srv.Close()

	out, err := runCLI(t,
		"models", "list",
		"--base-url", srv.URL, "--api-key", "ck_test", "--format", "text",
	)
	if err != nil {
		t.Fatal(err)
	}
	if out != "celeris-1\n" {
		t.Errorf("out = %q", out)
	}
}

func TestModelsListJSONLFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[{"id":"celeris-1","object":"model","created":1,"owned_by":"celeris"},{"id":"celeris-2","object":"model","created":2,"owned_by":"celeris"}]}`)
	}))
	defer srv.Close()

	out, err := runCLI(t,
		"models", "list",
		"--base-url", srv.URL, "--api-key", "ck_test", "--format", "jsonl",
	)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "celeris-1") || !strings.Contains(lines[1], "celeris-2") {
		t.Errorf("out = %q", out)
	}
}

func TestAPICommandRejectsBadMethod(t *testing.T) {
	_, err := runCLI(t, "api", "yeet", "/models", "--api-key", "ck_test")
	if err == nil || !strings.Contains(err.Error(), "unsupported method") {
		t.Errorf("got %v", err)
	}
}

func TestUnknownFormatIsUsageError(t *testing.T) {
	_, err := runCLI(t, "models", "list", "--api-key", "ck_test", "--format", "xml")
	if err == nil || !strings.Contains(err.Error(), "unknown --format") {
		t.Errorf("got %v", err)
	}
	if _, ok := err.(usageError); !ok {
		t.Errorf("want usageError, got %T", err)
	}
}

func TestVersionCommand(t *testing.T) {
	out, err := runCLI(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "celeris ") {
		t.Errorf("out = %q", out)
	}
}
