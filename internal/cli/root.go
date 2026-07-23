// Package cli wires the celeris command tree. Commands follow the
// openai-cli resource style: `celeris [resource] <command> [flags]`.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ai-celeris/celeris-cli/internal/api"
	"github.com/ai-celeris/celeris-cli/internal/version"
	"github.com/spf13/cobra"
)

// usageError marks errors that should exit 2 (bad invocation) rather than 1
// (request failed).
type usageError struct{ error }

func usageErrorf(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}

type rootOptions struct {
	apiKey  string
	baseURL string
	format  string
	debug   bool
	timeout time.Duration
	headers []string
}

func envOr(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func (o *rootOptions) resolvedAPIKey() string {
	if o.apiKey != "" {
		return o.apiKey
	}
	return envOr("CELERIS_API_KEY", "OPENAI_API_KEY")
}

func (o *rootOptions) resolvedBaseURL() string {
	if o.baseURL != "" {
		return o.baseURL
	}
	return envOr("CELERIS_BASE_URL", "OPENAI_BASE_URL")
}

func (o *rootOptions) client() (*api.Client, error) {
	var debug *os.File
	if o.debug {
		debug = os.Stderr
	}
	headers, err := parseHeaders(o.headers)
	if err != nil {
		return nil, err
	}
	// The HTTP client carries no timeout of its own: streams must be able to
	// run indefinitely. Non-streaming calls get a context deadline instead.
	return api.New(o.resolvedBaseURL(), o.resolvedAPIKey(), 0, debug).WithHeaders(headers), nil
}

func parseHeaders(raw []string) (http.Header, error) {
	headers := make(http.Header, len(raw))
	for _, entry := range raw {
		name, value, ok := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || !validHeaderName(name) || !validHeaderValue(value) {
			return nil, usageErrorf("invalid header %q: expected \"Name: value\"", entry)
		}
		headers.Set(name, value)
	}
	return headers, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	const separators = `()<>@,;:\"/[]?={} ` + "\t"
	for i := 0; i < len(name); i++ {
		if name[i] <= 0x20 || name[i] >= 0x7f || strings.ContainsRune(separators, rune(name[i])) {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if (value[i] < 0x20 && value[i] != '\t') || value[i] == 0x7f {
			return false
		}
	}
	return true
}

// requestContext applies --timeout to non-streaming calls.
func (o *rootOptions) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, o.timeout)
}

func defaultModel() string {
	if m := os.Getenv("CELERIS_MODEL"); m != "" {
		return m
	}
	return "celeris-1"
}

// validMaxTokens are the only values the service accepts (multiples of 256
// up to 1024); 0 means "omit from the request".
func validateMaxTokens(n int) error {
	switch n {
	case 0, 256, 512, 768, 1024:
		return nil
	}
	return usageErrorf("--max-tokens must be one of 256, 512, 768, 1024 (got %d)", n)
}

// NewRootCommand assembles the full command tree.
func NewRootCommand() *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:           "celeris",
		Short:         "Command-line interface for the Celeris inference API",
		Long:          "celeris talks to the Celeris low-latency inference API from the shell.\nInputs read from flags, @files, or stdin; results write to stdout.",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(version.Full() + "\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError{err}
	})

	pf := root.PersistentFlags()
	pf.StringVar(&opts.apiKey, "api-key", "", "API key (default $CELERIS_API_KEY, then $OPENAI_API_KEY)")
	pf.StringVar(&opts.baseURL, "base-url", "", "endpoint root, /v1 appended automatically (default $CELERIS_BASE_URL, then $OPENAI_BASE_URL, then "+api.DefaultBaseURL+")")
	pf.StringVar(&opts.format, "format", "auto", "output format: auto|text|json|jsonl|pretty|raw")
	pf.StringArrayVarP(&opts.headers, "header", "H", nil, "custom request header in \"Name: value\" form (repeatable)")
	pf.BoolVar(&opts.debug, "debug", false, "trace requests (method, URL, User-Agent, bodies) to stderr")
	pf.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "per-request timeout for non-streaming calls (0 disables)")

	root.AddCommand(
		newChatCompletionsCommand(opts),
		newCompletionsCommand(opts),
		newModelsCommand(opts),
		newQCommand(opts),
		newAPICommand(opts),
		newVersionCommand(),
	)
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, platform, and Go toolchain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Full())
			return nil
		},
	}
}

// Main runs the CLI and returns the process exit code: 0 success, 1 request
// or API failure, 2 usage error.
func Main() int {
	root := NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "celeris: %v\n", err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}
