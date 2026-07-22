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
# Optional; defaults to the production endpoint for the selected model:
export CELERIS_BASE_URL="https://inference.celeris.ai/celeris-1"
```

`OPENAI_API_KEY` / `OPENAI_BASE_URL` are honored as fallbacks, and every
setting has a flag (`--api-key`, `--base-url`). `CELERIS_MODEL` overrides the
default model (`celeris-1`).

Prefer the environment variable to `--api-key`: command arguments are visible
to other users via the process list and are saved in your shell history.

### Models live in the endpoint path

Production endpoints embed the model id: `https://inference.celeris.ai/<model>/v1`.
The body's `model` field must match that path segment, so **changing the model
changes the endpoint**. When you have not set `--base-url` or `$CELERIS_BASE_URL`,
the CLI derives the endpoint from `--model` and this takes care of itself:

```sh
celeris q -m celeris-2 "hello"        # → https://inference.celeris.ai/celeris-2/v1
```

If you *have* pinned a base URL and it names a different model than `--model`,
the CLI warns on stderr rather than letting the service reject the request with
an error that does not explain itself.

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
with an explicit `--format json` or `--format text` when it matters. `q` is the
exception: it prints plain text under `auto` whether or not stdout is a
terminal, since it exists for pipelines. An explicit `--format` still wins.

Celeris accepts `max_tokens` of 256, 512, 768, or 1024 only, and the CLI
rejects any other value before sending. The 4096-token context window covers
prompt plus completion together; the CLI does not count prompt tokens, so
exceeding the window surfaces as an API error rather than a local one.

Rate-limited (`429`) and `5xx` responses are retried automatically — twice by
default, honoring `Retry-After`. Tune with `--retry N`, or `--retry 0` to
disable. Streaming calls are never retried, because tokens already written to
stdout cannot be withdrawn.

Every request carries a `User-Agent` like
`celeris-cli/1.2.3 (darwin; arm64) go/1.23.4`, so server-side logs can
attribute traffic to a CLI version. `--debug` prints request and response
metadata to stderr; it never prints your API key, but it does print request
bodies, which contain your prompts.

Exit codes: `0` success, `1` request/API failure, `2` usage error.

## Verifying a download

Release archives ship with `checksums.txt`:

```sh
sha256sum -c checksums.txt --ignore-missing
```

Binaries are not code-signed or notarized. The Homebrew cask therefore strips
the macOS quarantine attribute on install so Gatekeeper does not block the
first run — which also means Gatekeeper is not vetting the binary for you. If
you would rather not accept that, build from source with `go install`.

## Develop

```sh
go test -race ./...
go build ./cmd/celeris
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for conventions, and
[SECURITY.md](SECURITY.md) to report a vulnerability.

Releases are tag-driven: pushing `vX.Y.Z` runs goreleaser, which publishes
archives for macOS/Linux/Windows and updates the Homebrew cask in
`ai-celeris/homebrew-tools`.
