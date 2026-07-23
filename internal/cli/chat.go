package cli

import (
	"strings"

	"github.com/ai-celeris/celeris-cli/internal/api"
	"github.com/spf13/cobra"
)

func parseMessageFlag(raw string) (api.ChatMessage, error) {
	role, content, ok := strings.Cut(raw, ":")
	if !ok {
		return api.ChatMessage{}, usageErrorf("--message %q must be role:content", raw)
	}
	switch role {
	case "system", "user", "assistant":
		return api.ChatMessage{Role: role, Content: content}, nil
	}
	return api.ChatMessage{}, usageErrorf("--message role %q must be system, user, or assistant", role)
}

func newChatCompletionsCommand(opts *rootOptions) *cobra.Command {
	group := &cobra.Command{
		Use:     "chat:completions",
		Short:   "Chat completions (POST /v1/chat/completions)",
		Aliases: []string{"chat.completions"},
	}

	var (
		model    string
		system   string
		input    string
		messages []string
		stream   bool
		user     string
		sampling samplingFlags
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a chat completion",
		Example: `  celeris chat:completions create -i "Classify as positive or negative: great product" --max-tokens 256
  git diff | celeris chat:completions create --system "Write a commit message for this diff." -i - --stream
  celeris chat:completions create -g system:"Answer tersely." -g user:"What is a monad?"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFormat(opts.format); err != nil {
				return err
			}
			if err := sampling.validate(); err != nil {
				return err
			}
			var msgs []api.ChatMessage
			if system != "" {
				msgs = append(msgs, api.ChatMessage{Role: "system", Content: system})
			}
			for _, raw := range messages {
				m, err := parseMessageFlag(raw)
				if err != nil {
					return err
				}
				msgs = append(msgs, m)
			}
			userContent, ok, err := resolveInput(input)
			if err != nil {
				return err
			}
			if ok {
				msgs = append(msgs, api.ChatMessage{Role: "user", Content: userContent})
			}
			if len(msgs) == 0 {
				return usageErrorf("no messages: pass --input/-i, --message/-g, or pipe stdin")
			}
			req := api.ChatCompletionRequest{
				Model:            model,
				Messages:         msgs,
				MaxTokens:        sampling.maxTokens,
				Temperature:      floatIfSet(cmd, "temperature", sampling.temperature),
				TopP:             floatIfSet(cmd, "top-p", sampling.topP),
				Seed:             intIfSet(cmd, "seed", sampling.seed),
				Stop:             sampling.stop,
				PresencePenalty:  floatIfSet(cmd, "presence-penalty", sampling.presencePenalty),
				FrequencyPenalty: floatIfSet(cmd, "frequency-penalty", sampling.frequencyPenalty),
				User:             user,
			}
			client, err := opts.client()
			if err != nil {
				return err
			}
			if stream {
				handler, finish := streamRenderer(cmd.OutOrStdout(), opts.format)
				if err := client.ChatCompletionStream(cmd.Context(), req, handler); err != nil {
					return err
				}
				finish()
				return nil
			}
			ctx, cancel := opts.requestContext(cmd.Context())
			defer cancel()
			body, err := client.ChatCompletion(ctx, req)
			if err != nil {
				return err
			}
			return renderBody(cmd.OutOrStdout(), opts.format, body, chatText)
		},
	}
	f := create.Flags()
	f.StringVarP(&model, "model", "m", defaultModel(), "model id (default $CELERIS_MODEL, then celeris-1)")
	f.StringVarP(&input, "input", "i", "", "user message: literal text, @file, or - for stdin (piped stdin is used when omitted)")
	f.StringVar(&system, "system", "", "system message prepended to the conversation")
	f.StringArrayVarP(&messages, "message", "g", nil, "message as role:content (repeatable, in order)")
	f.BoolVar(&stream, "stream", false, "stream tokens as they are generated (SSE)")
	f.StringVar(&user, "user", "", "end-user identifier forwarded to the API")
	sampling.register(create)

	group.AddCommand(create)
	return group
}
