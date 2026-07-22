package cli

import (
	"github.com/ai-celeris/celeris-cli/internal/api"
	"github.com/spf13/cobra"
)

func newCompletionsCommand(opts *rootOptions) *cobra.Command {
	group := &cobra.Command{
		Use:   "completions",
		Short: "Legacy text completions (POST /v1/completions)",
	}

	var (
		model    string
		prompt   string
		stream   bool
		sampling samplingFlags
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a text completion",
		Example: `  celeris completions create -p "The capital of France is" --max-tokens 256
  celeris completions create -p @prompt.txt --stream`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFormat(opts.format); err != nil {
				return err
			}
			if err := sampling.validate(); err != nil {
				return err
			}
			text, ok, err := resolveInput(prompt)
			if err != nil {
				return err
			}
			if !ok {
				return usageErrorf("no prompt: pass --prompt/-p or pipe stdin")
			}
			req := api.CompletionRequest{
				Model:            model,
				Prompt:           text,
				MaxTokens:        sampling.maxTokens,
				Temperature:      floatIfSet(cmd, "temperature", sampling.temperature),
				TopP:             floatIfSet(cmd, "top-p", sampling.topP),
				Seed:             intIfSet(cmd, "seed", sampling.seed),
				Stop:             sampling.stop,
				PresencePenalty:  floatIfSet(cmd, "presence-penalty", sampling.presencePenalty),
				FrequencyPenalty: floatIfSet(cmd, "frequency-penalty", sampling.frequencyPenalty),
			}
			client := opts.client()
			if stream {
				handler, finish := streamRenderer(cmd.OutOrStdout(), opts.format)
				if err := client.CompletionStream(cmd.Context(), req, handler); err != nil {
					return err
				}
				finish()
				return nil
			}
			ctx, cancel := opts.requestContext(cmd.Context())
			defer cancel()
			body, err := client.Completion(ctx, req)
			if err != nil {
				return err
			}
			return renderBody(cmd.OutOrStdout(), opts.format, body, completionText)
		},
	}
	f := create.Flags()
	f.StringVarP(&model, "model", "m", defaultModel(), "model id (default $CELERIS_MODEL, then celeris-1)")
	f.StringVarP(&prompt, "prompt", "p", "", "prompt: literal text, @file, or - for stdin (piped stdin is used when omitted)")
	f.BoolVar(&stream, "stream", false, "stream tokens as they are generated (SSE)")
	sampling.register(create)

	group.AddCommand(create)
	return group
}
