// Package cli wires the celeris command tree. Commands follow the
// openai-cli resource style: `celeris [resource] <command> [flags]`.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	retries int
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

// resolvedBaseURL picks the endpoint for a request. Production embeds the
// model id in the path, so when nothing is configured the default endpoint is
// derived from the model rather than pinned to celeris-1.
func (o *rootOptions) resolvedBaseURL(model string) string {
	if o.baseURL != "" {
		return o.baseURL
	}
	if v := envOr("CELERIS_BASE_URL", "OPENAI_BASE_URL"); v != "" {
		return v
	}
	return api.DefaultBaseURLForModel(model)
}

// clientForModel builds a client aimed at the endpoint serving model.
// Commands with no model concept (models, api) pass "".
func (o *rootOptions) clientForModel(model string) *api.Client {
	var debug io.Writer
	if o.debug {
		debug = os.Stderr
	}
	// The HTTP client carries no timeout of its own: streams must be able to
	// run indefinitely. Non-streaming calls get a context deadline instead.
	return api.New(o.resolvedBaseURL(model), o.resolvedAPIKey(), 0, debug).
		WithRetries(o.retries)
}

// warnModelPathMismatch flags the case where an explicitly configured
// endpoint pins one model in its path while --model selects another: the
// service rejects that combination, and the resulting error does not say why.
// It is a warning rather than an error because a proxy may legitimately use a
// path that only looks like a model segment.
func warnModelPathMismatch(w io.Writer, o *rootOptions, model string) {
	if model == "" {
		return
	}
	seg := api.ModelPathSegment(o.resolvedBaseURL(model))
	if seg != "" && seg != model {
		fmt.Fprintf(w, "celeris: warning: endpoint path serves model %q but --model is %q; "+
			"set --base-url to the endpoint for %q\n", seg, model, model)
	}
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
	return api.DefaultModel
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
	pf.StringVar(&opts.baseURL, "base-url", "", "endpoint root, /v1 appended automatically (default $CELERIS_BASE_URL, then $OPENAI_BASE_URL, then "+api.DefaultHost+"/<model>)")
	pf.StringVar(&opts.format, "format", "auto", "output format: auto|text|json|jsonl|pretty|raw")
	pf.BoolVar(&opts.debug, "debug", false, "trace requests (method, URL, User-Agent, bodies) to stderr")
	pf.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "per-request timeout for non-streaming calls (0 disables)")
	pf.IntVar(&opts.retries, "retry", 2, "retries for rate-limited (429) and 5xx responses on non-streaming calls")

	root.AddCommand(
		newChatCompletionsCommand(opts),
		newCompletionsCommand(opts),
		newModelsCommand(opts),
		newQCommand(opts),
		newAPICommand(opts),
		newVersionCommand(),
	)

	// Cobra's built-in `completion` sits next to the `completions` API
	// resource in help output; spell out the difference so the two are not
	// mistaken for each other.
	root.InitDefaultCompletionCmd()
	if c, _, err := root.Find([]string{"completion"}); err == nil && c != nil {
		c.Short = "Generate a shell autocompletion script (not the completions API)"
	}
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
