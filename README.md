# celeris-cli

`celeris` is the command-line interface for the [Celeris](https://docs.celeris.ai)
low-latency inference API. It follows the resource-based command style of the
OpenAI CLI and is built for shell pipelines: inputs come from flags, `@files`,
or stdin; results go to stdout; diagnostics go to stderr.

## Install

Homebrew:

```sh
brew install ai-celeris/tools/celeris
```

Go (1.23+):

```sh
go install github.com/ai-celeris/celeris-cli/cmd/celeris@latest
```

Or download a binary archive from the
[releases page](https://github.com/ai-celeris/celeris-cli/releases).

## Configure

```sh
export CELERIS_API_KEY="ck_..."               # from https://console.celeris.ai
# Optional; defaults to the production Celeris-1 endpoint:
export CELERIS_BASE_URL="https://inference.cloud.celeris.ai/celeris-1"
```

`OPENAI_API_KEY` / `OPENAI_BASE_URL` are honored as fallbacks, and every
setting has a flag (`--api-key`, `--base-url`). `CELERIS_MODEL` overrides the
default model (`celeris-1`).

## Use

```sh
# Quick pipeline answers (streams plain text):
celeris q "Three rhymes for shell"
git diff --staged | celeris q "Write a one-line commit message for this diff:"

# Full chat completions API:
celeris chat:completions create -i "Classify as positive or negative: great product" --max-tokens 256
celeris chat:completions create --system "Answer tersely." -i @question.txt --stream
celeris chat:completions create -g system:"Be brief." -g user:"What is a monad?" --format json

# Legacy completions:
celeris completions create -p "The capital of France is" --max-tokens 256

# Models:
celeris models list

# Raw escape hatch for anything else under /v1:
celeris api get /models
echo '{"model":"celeris-1","messages":[{"role":"user","content":"hi"}]}' | celeris api post /chat/completions
```

Output format is controlled by `--format` (`auto`, `text`, `json`, `jsonl`,
`pretty`, `raw`). `auto` prints pretty JSON on a terminal and bare text when
piped, so `celeris ... | jq` and `celeris ... | xargs` both do what you mean
with an explicit `--format json` or `--format text` when it matters.

Note that Celeris accepts `max_tokens` of 256, 512, 768, or 1024 only, and
prompt plus completion must fit the 4096-token window; the CLI validates this
before sending.

Every request carries a `User-Agent` like
`celeris-cli/1.2.3 (darwin; arm64) go/1.23.4`, so server-side logs can
attribute traffic to a CLI version. `--debug` prints the request and response
metadata to stderr.

Exit codes: `0` success, `1` request/API failure, `2` usage error.

## Develop

```sh
go test ./...
go build ./cmd/celeris
```

Releases are tag-driven: pushing `vX.Y.Z` runs goreleaser, which publishes
archives for macOS/Linux/Windows and updates the Homebrew formula in
`ai-celeris/homebrew-tools`.
