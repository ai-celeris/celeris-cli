package cli

import (
	"strings"

	"github.com/ai-celeris/celeris-cli/internal/api"
	"github.com/spf13/cobra"
)

// newQCommand is the shell-pipeline shortcut: one word, streams text.
// `celeris q "question"` sends a chat completion; piped stdin becomes
// context appended after the question.
func newQCommand(opts *rootOptions) *cobra.Command {
	var (
		model    string
		system   string
		noStream bool
		sampling samplingFlags
	)
	cmd := &cobra.Command{
		Use:   "q [prompt...]",
		Short: "Quick chat completion for pipelines (streams plain text)",
		Long: "q is shorthand for `chat:completions create --stream` with text output.\n" +
			"Positional arguments form the prompt; piped stdin is appended as context.\n" +
			"With no arguments, piped stdin is the prompt.",
		Example: `  celeris q "Three rhymes for shell"
  git diff --staged | celeris q "Write a one-line commit message for this diff:"
  tail -100 app.log | celeris q "Summarize the errors in this log:"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sampling.validate(); err != nil {
				return err
			}
			prompt := strings.TrimSpace(strings.Join(args, " "))
			if stdinIsPiped() {
				stdin, err := readAllStdin()
				if err != nil {
					return err
				}
				stdin = strings.TrimSpace(stdin)
				switch {
				case prompt == "":
					prompt = stdin
				case stdin != "":
					prompt = prompt + "\n\n" + stdin
				}
			}
			if prompt == "" {
				return usageErrorf("no prompt: pass arguments or pipe stdin")
			}
			var msgs []api.ChatMessage
			if system != "" {
				msgs = append(msgs, api.ChatMessage{Role: "system", Content: system})
			}
			msgs = append(msgs, api.ChatMessage{Role: "user", Content: prompt})
			maxTokens := sampling.maxTokens
			if maxTokens == 0 {
				// q favors interactive latency; 256 is the smallest budget the
				// service accepts and plenty for pipeline-sized answers.
				maxTokens = 256
			}
			req := api.ChatCompletionRequest{
				Model:            model,
				Messages:         msgs,
				MaxTokens:        maxTokens,
				Temperature:      floatIfSet(cmd, "temperature", sampling.temperature),
				TopP:             floatIfSet(cmd, "top-p", sampling.topP),
				Seed:             intIfSet(cmd, "seed", sampling.seed),
				Stop:             sampling.stop,
				PresencePenalty:  floatIfSet(cmd, "presence-penalty", sampling.presencePenalty),
				FrequencyPenalty: floatIfSet(cmd, "frequency-penalty", sampling.frequencyPenalty),
			}
			client, err := opts.client()
			if err != nil {
				return err
			}
			if noStream {
				ctx, cancel := opts.requestContext(cmd.Context())
				defer cancel()
				body, err := client.ChatCompletion(ctx, req)
				if err != nil {
					return err
				}
				return renderBody(cmd.OutOrStdout(), "text", body, chatText)
			}
			handler, finish := streamRenderer(cmd.OutOrStdout(), "text")
			if err := client.ChatCompletionStream(cmd.Context(), req, handler); err != nil {
				return err
			}
			finish()
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&model, "model", "m", defaultModel(), "model id (default $CELERIS_MODEL, then celeris-1)")
	f.StringVar(&system, "system", "", "system message")
	f.BoolVar(&noStream, "no-stream", false, "wait for the full response instead of streaming")
	sampling.register(cmd)
	return cmd
}
