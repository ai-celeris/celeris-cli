package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/ai-celeris/celeris-cli/internal/api"
	"github.com/spf13/cobra"
)

func newModelsCommand(opts *rootOptions) *cobra.Command {
	group := &cobra.Command{
		Use:   "models",
		Short: "Models (GET /v1/models)",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the models the endpoint serves",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFormat(opts.format); err != nil {
				return err
			}
			ctx, cancel := opts.requestContext(cmd.Context())
			defer cancel()
			body, err := opts.clientForModel("").Models(ctx)
			if err != nil {
				return err
			}

			var models api.ModelList
			parseErr := json.Unmarshal(body, &models)
			w := cmd.OutOrStdout()
			switch effectiveFormat(opts.format) {
			case "text":
				if parseErr != nil {
					return fmt.Errorf("parsing response: %w", parseErr)
				}
				for _, m := range models.Data {
					fmt.Fprintln(w, m.ID)
				}
				return nil
			case "pretty":
				if parseErr != nil || !stdoutIsTTY() {
					return writeJSON(w, body, true)
				}
				tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tOWNED BY\tCREATED")
				for _, m := range models.Data {
					created := ""
					if m.Created > 0 {
						created = time.Unix(m.Created, 0).UTC().Format(time.DateOnly)
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", m.ID, m.OwnedBy, created)
				}
				return tw.Flush()
			case "raw":
				ensureTrailingNewline(w, string(body))
				return nil
			case "jsonl":
				if parseErr == nil && len(models.Data) > 0 {
					// One model object per line for line-oriented tooling.
					var page struct {
						Data []json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(body, &page); err == nil {
						for _, m := range page.Data {
							if err := writeJSON(w, m, false); err != nil {
								return err
							}
						}
						return nil
					}
				}
				return writeJSON(w, body, false)
			default: // json
				return writeJSON(w, body, false)
			}
		},
	}

	group.AddCommand(list)
	return group
}
