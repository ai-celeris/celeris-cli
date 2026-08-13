package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryStore struct{ creds Credentials }

func (m *memoryStore) Load() (Credentials, error) { return m.creds, nil }
func (m *memoryStore) Save(creds Credentials) error {
	m.creds = creds
	return nil
}

func TestLoginOpensBrowserPollsAndSavesKey(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/agent/device":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["keyName"] != LoginKeyName {
				t.Errorf("device request = %#v, %v", request, err)
			}
			fmt.Fprintf(w, `{"deviceCode":"device-secret","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/?agent_authorization=signed-public-code")
		case "/auth/agent/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending","error_description":"waiting"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"ck_secret","token_type":"Bearer","management_token":"cmt_secret.signed","management_token_type":"Bearer","workspace_id":"ws_1","workspace_name":"Acme Prod"}`)
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
	if store.creds.APIKey != "ck_secret" {
		t.Fatalf("stored key = %q", store.creds.APIKey)
	}
	if store.creds.ManagementToken != "cmt_secret.signed" {
		t.Fatalf("stored management token = %q", store.creds.ManagementToken)
	}
	if store.creds.WorkspaceID != "ws_1" || store.creds.WorkspaceName != "Acme Prod" {
		t.Fatalf("stored workspace = %q/%q", store.creds.WorkspaceID, store.creds.WorkspaceName)
	}
	if opened != srv.URL+"/?agent_authorization=signed-public-code" {
		t.Fatalf("opened = %q", opened)
	}
	if !strings.Contains(diagnostics.String(), "ABCD-2345") || strings.Contains(diagnostics.String(), "device-secret") || strings.Contains(diagnostics.String(), "ck_secret") {
		t.Fatalf("unsafe diagnostics = %q", diagnostics.String())
	}
	if !strings.Contains(out.String(), "Credentials saved to the OS keychain") || !strings.Contains(out.String(), "Acme Prod") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestLoginPrintsURLWhenBrowserCannotOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/agent/device" {
			fmt.Fprintf(w, `{"deviceCode":"device","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/verify")
			return
		}
		fmt.Fprint(w, `{"access_token":"ck_secret","token_type":"Bearer","management_token":"cmt_secret.signed","management_token_type":"Bearer"}`)
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
	if store.creds.APIKey != "" {
		t.Fatalf("stored malformed secret %q", store.creds.APIKey)
	}
}

func TestLoginNeverStoresMalformedManagementCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/agent/device" {
			fmt.Fprintf(w, `{"deviceCode":"device","userCode":"ABCD-2345","verificationUriComplete":%q,"expiresIn":600,"interval":5}`, srvURL(r)+"/verify")
			return
		}
		fmt.Fprint(w, `{"access_token":"ck_secret","token_type":"Bearer","management_token":"cmt_secret.signed","management_token_type":"Basic"}`)
	}))
	defer srv.Close()
	store := &memoryStore{}
	err := Login(context.Background(), Config{
		ConsoleURL: srv.URL,
		HTTPClient: srv.Client(),
		Store:      store,
		Open:       func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid management token") {
		t.Fatalf("err = %v", err)
	}
	if store.creds.APIKey != "" || store.creds.ManagementToken != "" {
		t.Fatalf("stored malformed credentials %#v", store.creds)
	}
}

func TestWriteStatus(t *testing.T) {
	cases := []struct {
		name  string
		creds Credentials
		want  string
	}{
		{"logged out", Credentials{}, "Not logged in"},
		{"named workspace", Credentials{APIKey: "ck_x", WorkspaceName: "Acme Prod", WorkspaceID: "ws_1"}, `"Acme Prod"`},
		{"id only", Credentials{APIKey: "ck_x", WorkspaceID: "ws_1"}, `"ws_1"`},
		{"key without workspace", Credentials{APIKey: "ck_x"}, "Logged in."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			WriteStatus(&out, tc.creds)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("status = %q, want substring %q", out.String(), tc.want)
			}
		})
	}
}

func TestDecodeCredentialsAcceptsLegacyBareKey(t *testing.T) {
	if got := decodeCredentials("ck_bare"); got.APIKey != "ck_bare" || got.WorkspaceName != "" {
		t.Fatalf("bare key decoded to %#v", got)
	}
	if got := decodeCredentials(`{"apiKey":"ck_json","workspaceName":"Acme"}`); got.APIKey != "ck_json" || got.WorkspaceName != "Acme" {
		t.Fatalf("json decoded to %#v", got)
	}
	if got := decodeCredentials(`{"apiKey":"ck_json","managementToken":"cmt_json.signed"}`); got.ManagementToken != "cmt_json.signed" {
		t.Fatalf("management token decoded to %#v", got)
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}
