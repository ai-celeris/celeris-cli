package auth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryStore struct{ secret string }

func (m *memoryStore) Get() (string, error) { return m.secret, nil }
func (m *memoryStore) Set(secret string) error {
	m.secret = secret
	return nil
}

func TestLoginOpensBrowserPollsAndSavesKey(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/agent/device":
			fmt.Fprintf(w, `{"deviceCode":"device-secret","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/?agent_authorization=signed-public-code")
		case "/auth/agent/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending","error_description":"waiting"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"ck_secret","token_type":"Bearer"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := &memoryStore{}
	var opened string
	var out, diagnostics bytes.Buffer
	err := Login(context.Background(), Config{
		ConsoleURL: srv.URL,
		HTTPClient: srv.Client(),
		Store:      store,
		Open: func(target string) error {
			opened = target
			return nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
		Out:   &out,
		Err:   &diagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.secret != "ck_secret" {
		t.Fatalf("stored secret = %q", store.secret)
	}
	if opened != srv.URL+"/?agent_authorization=signed-public-code" {
		t.Fatalf("opened = %q", opened)
	}
	if !strings.Contains(diagnostics.String(), "ABCD-2345") || strings.Contains(diagnostics.String(), "device-secret") || strings.Contains(diagnostics.String(), "ck_secret") {
		t.Fatalf("unsafe diagnostics = %q", diagnostics.String())
	}
	if !strings.Contains(out.String(), "OS keychain") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestLoginPrintsURLWhenBrowserCannotOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/agent/device" {
			fmt.Fprintf(w, `{"deviceCode":"device","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/verify")
			return
		}
		fmt.Fprint(w, `{"access_token":"ck_secret","token_type":"Bearer"}`)
	}))
	defer srv.Close()
	var diagnostics bytes.Buffer
	err := Login(context.Background(), Config{
		ConsoleURL: srv.URL,
		HTTPClient: srv.Client(),
		Store:      &memoryStore{},
		Open:       func(string) error { return fmt.Errorf("no browser") },
		Sleep:      func(context.Context, time.Duration) error { return nil },
		Err:        &diagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), srv.URL+"/verify") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestLoginRejectsNonLocalPlainHTTP(t *testing.T) {
	err := Login(context.Background(), Config{ConsoleURL: "http://example.com"})
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginRejectsCrossOriginVerificationURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"deviceCode":"device","userCode":"ABCD-2345","verificationUriComplete":"https://evil.example/verify","expiresIn":600,"interval":5}`)
	}))
	defer srv.Close()
	err := Login(context.Background(), Config{ConsoleURL: srv.URL, HTTPClient: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "configured console origin") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginNeverStoresMalformedCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/agent/device" {
			fmt.Fprintf(w, `{"deviceCode":"device","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/verify")
			return
		}
		fmt.Fprint(w, `{"access_token":"not-a-celeris-key","token_type":"Bearer"}`)
	}))
	defer srv.Close()
	store := &memoryStore{}
	err := Login(context.Background(), Config{
		ConsoleURL: srv.URL,
		HTTPClient: srv.Client(),
		Store:      store,
		Open:       func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("err = %v", err)
	}
	if store.secret != "" {
		t.Fatalf("stored malformed secret %q", store.secret)
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}
