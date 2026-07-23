package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// newAPICommand is the escape hatch for endpoints the typed commands do not
// cover: an authenticated raw request under the /v1 base.
func newAPICommand(opts *rootOptions) *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "api <method> <path>",
		Short: "Raw authenticated request under /v1 (escape hatch)",
		Example: `  celeris api get /models
  celeris api post /chat/completions --data @request.json
  echo '{"model":"celeris-1","messages":[{"role":"user","content":"hi"}]}' | celeris api post /chat/completions`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkFormat(opts.format); err != nil {
				return err
			}
			method := strings.ToUpper(args[0])
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				return usageErrorf("unsupported method %q", args[0])
			}
			var body []byte
			// GET and DELETE take no body. Without this guard the implicit
			// stdin fallback attaches whatever the pipeline happens to be
			// feeding the process, producing a GET with a payload that
			// proxies and servers are entitled to reject.
			if method == "GET" || method == "DELETE" {
				if data != "" {
					return usageErrorf("--data is not valid for a %s request", method)
				}
			} else {
				payload, ok, err := resolveInput(data)
				if err != nil {
					return err
				}
				if ok {
					body = []byte(payload)
				}
			}
			ctx, cancel := opts.requestContext(cmd.Context())
			defer cancel()
			client, err := opts.clientForModel("")
			if err != nil {
				return err
			}
			resp, err := client.Raw(ctx, method, args[1], body)
			if err != nil {
				return err
			}
			format := opts.format
			if format == "auto" && !stdoutIsTTY() {
				// Raw API output is JSON, not prose; auto never means "text" here.
				format = "json"
			}
			return renderBody(cmd.OutOrStdout(), format, resp, func(b []byte) (string, error) {
				return string(b), nil
			})
		},
	}
	cmd.Flags().StringVarP(&data, "data", "d", "", "request body: literal JSON, @file, or - for stdin (piped stdin is used when omitted)")
	return cmd
}
