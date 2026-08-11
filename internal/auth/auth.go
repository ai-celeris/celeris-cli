// Package auth handles browser authorization and OS-keychain credentials.
package auth

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
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	DefaultConsoleURL = "https://console.celeris.ai"
	LoginKeyName      = "Celeris CLI"
	keychainService   = "celeris-cli"
	keychainAccount   = "api-key"
	maxResponseBytes  = 1 << 20
)

// Credentials are what a login persists: the minted inference API key plus the
// workspace the member chose to mint it into at approval time. The workspace
// fields are display metadata so the CLI can show which workspace the stored
// key belongs to; they are never sent as an authorization input.
type Credentials struct {
	APIKey        string `json:"apiKey"`
	WorkspaceID   string `json:"workspaceId,omitempty"`
	WorkspaceName string `json:"workspaceName,omitempty"`
}

// CredentialStore is the seam between authorization and durable secret storage.
type CredentialStore interface {
	Load() (Credentials, error)
	Save(Credentials) error
}

// Keychain stores the credentials in the operating system's native credential
// store, as one JSON blob under a single account.
type Keychain struct{}

func (Keychain) Load() (Credentials, error) {
	raw, err := keyring.Get(keychainService, keychainAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credentials{}, nil
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("read OS keychain: %w", err)
	}
	return decodeCredentials(raw), nil
}

func (Keychain) Save(creds Credentials) error {
	blob, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := keyring.Set(keychainService, keychainAccount, string(blob)); err != nil {
		return fmt.Errorf("save API key to OS keychain: %w", err)
	}
	return nil
}

// decodeCredentials reads a stored credential blob. A bare, non-JSON value is
// treated as an API key with no workspace metadata, so a credential written by
// an earlier build (which stored the key alone) still loads.
func decodeCredentials(raw string) Credentials {
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		var creds Credentials
		if err := json.Unmarshal([]byte(raw), &creds); err == nil {
			return creds
		}
	}
	return Credentials{APIKey: raw}
}

// WriteStatus renders the stored login for humans: which workspace the saved
// key is connected to, or that there is no saved key.
func WriteStatus(w io.Writer, creds Credentials) {
	if creds.APIKey == "" {
		fmt.Fprintln(w, "Not logged in. Run `celeris login`.")
		return
	}
	name := creds.WorkspaceName
	if name == "" {
		name = creds.WorkspaceID
	}
	if name != "" {
		fmt.Fprintf(w, "Logged in to workspace %q.\n", name)
		return
	}
	fmt.Fprintln(w, "Logged in.")
}

// Config supplies the adapters used during a login. Tests replace all three.
type Config struct {
	ConsoleURL string
	HTTPClient *http.Client
	Store      CredentialStore
	Open       func(string) error
	Sleep      func(context.Context, time.Duration) error
	Out        io.Writer
	Err        io.Writer
}

type deviceStart struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int64  `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	// WorkspaceID and WorkspaceName name the workspace the member chose in the
	// browser to mint this key into (workspace selection, CEL-209). They are
	// display metadata, not an authorization input.
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Error         string `json:"error"`
	Description   string `json:"error_description"`
}

// Login runs the device authorization flow and saves the one-time API key.
// The device code and API key are never printed or passed to the browser.
func Login(ctx context.Context, cfg Config) error {
	cfg = defaults(cfg)
	base, err := validateConsoleURL(cfg.ConsoleURL)
	if err != nil {
		return err
	}

	var started deviceStart
	if err := postJSON(ctx, cfg.HTTPClient, base+"/auth/agent/device", map[string]string{"keyName": LoginKeyName}, &started); err != nil {
		return fmt.Errorf("start login: %w", err)
	}
	if started.DeviceCode == "" || started.UserCode == "" || started.VerificationURIComplete == "" || started.ExpiresIn <= 0 {
		return errors.New("start login: console returned an incomplete authorization")
	}
	verificationURL, err := validateConsoleURL(started.VerificationURIComplete)
	if err != nil {
		return fmt.Errorf("start login: unsafe verification URL: %w", err)
	}
	if !sameOrigin(base, verificationURL) {
		return errors.New("start login: verification URL is not on the configured console origin")
	}

	fmt.Fprintf(cfg.Err, "Pairing code: %s\n", started.UserCode)
	if err := cfg.Open(started.VerificationURIComplete); err != nil {
		fmt.Fprintf(cfg.Err, "Open this URL to continue:\n%s\n", started.VerificationURIComplete)
	} else {
		fmt.Fprintln(cfg.Err, "Opened your browser. Approve the connection there.")
	}

	deadline := time.Now().Add(time.Duration(started.ExpiresIn) * time.Second)
	interval := time.Duration(started.Interval) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	for {
		if time.Now().After(deadline) {
			return errors.New("login expired; run `celeris login` again")
		}
		var token tokenResponse
		status, err := postJSONStatus(ctx, cfg.HTTPClient, base+"/auth/agent/token", map[string]string{"deviceCode": started.DeviceCode}, &token)
		if err != nil {
			return fmt.Errorf("poll login: %w", err)
		}
		if status >= 200 && status < 300 {
			if !strings.EqualFold(token.TokenType, "Bearer") || !strings.HasPrefix(token.AccessToken, "ck_") {
				return errors.New("finish login: console returned an invalid API key")
			}
			if err := cfg.Store.Save(Credentials{
				APIKey:        token.AccessToken,
				WorkspaceID:   token.WorkspaceID,
				WorkspaceName: token.WorkspaceName,
			}); err != nil {
				return err
			}
			if token.WorkspaceName != "" {
				fmt.Fprintf(cfg.Out, "Logged in to workspace %q. API key saved to the OS keychain.\n", token.WorkspaceName)
			} else {
				fmt.Fprintln(cfg.Out, "Logged in. API key saved to the OS keychain.")
			}
			return nil
		}
		switch token.Error {
		case "authorization_pending":
			// Keep the advertised interval.
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return errors.New("login denied in the browser")
		case "expired_token":
			return errors.New("login expired; run `celeris login` again")
		default:
			if token.Description == "" {
				token.Description = http.StatusText(status)
			}
			return fmt.Errorf("login failed: %s", token.Description)
		}
		if err := cfg.Sleep(ctx, interval); err != nil {
			return err
		}
	}
}

func defaults(cfg Config) Config {
	if cfg.ConsoleURL == "" {
		cfg.ConsoleURL = DefaultConsoleURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.Store == nil {
		cfg.Store = Keychain{}
	}
	if cfg.Open == nil {
		cfg.Open = OpenBrowser
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.Err == nil {
		cfg.Err = io.Discard
	}
	return cfg
}

func validateConsoleURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("invalid console URL %q", raw)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if host != "localhost" && net.ParseIP(host) == nil {
			return "", errors.New("console URL must use HTTPS (HTTP is allowed only for loopback development)")
		}
		if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
			return "", errors.New("console URL must use HTTPS (HTTP is allowed only for loopback development)")
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func sameOrigin(left, right string) bool {
	l, lerr := url.Parse(left)
	r, rerr := url.Parse(right)
	return lerr == nil && rerr == nil && strings.EqualFold(l.Scheme, r.Scheme) && strings.EqualFold(l.Host, r.Host)
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, request, response any) error {
	status, err := postJSONStatus(ctx, client, endpoint, request, response)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("console returned HTTP %d", status)
	}
	return nil
}

func postJSONStatus(ctx context.Context, client *http.Client, endpoint string, request, response any) (int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(limited).Decode(response); err != nil {
		return resp.StatusCode, fmt.Errorf("decode console response: %w", err)
	}
	return resp.StatusCode, nil
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OpenBrowser opens a URL without involving a shell, keeping the signed URL
// out of shell history and avoiding shell interpretation.
func OpenBrowser(target string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{target}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		command, args = "xdg-open", []string{target}
	}
	return exec.Command(command, args...).Start()
}
