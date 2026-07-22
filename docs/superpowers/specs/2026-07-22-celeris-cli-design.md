# Celeris CLI design

Date: 2026-07-22
Status: approved for implementation (autonomous session; decisions recorded here)

## Purpose

A Go CLI (`celeris`) that lets users call the Celeris inference API from a shell,
modelled on `github.com/openai/openai-cli`. It must pipe cleanly (stdin in,
stdout out, diagnostics to stderr), stream tokens with low latency, and send a
custom `User-Agent` carrying the CLI version for request debugging.

## Constraints discovered from the platform

- Celeris exposes an OpenAI-compatible API at `<base>/v1`. The production
  endpoint is `https://inference.cloud.celeris.ai/celeris-1`; `CELERIS_BASE_URL`
  is the endpoint root *without* `/v1` (cookbook convention), and the CLI
  appends `/v1` unless the URL already ends with it.
- Supported surface (from `test_openai_compat_e2e.py` contract manifest):
  `models.list`, `chat.completions.create`, `chat.completions.stream` (+ common
  text parameters), `completions.create`, `completions.stream`, and
  authentication errors. `models.retrieve`, `responses.*`, tools, structured
  outputs, logprobs, `n>1`, and embeddings are NOT supported — the CLI does not
  expose them.
- Default model: `celeris-1`. Total token window 4096 (prompt + max_tokens);
  `max_tokens` must be one of {256, 512, 768, 1024} — the service rejects other
  values, so the CLI validates client-side with a clear message.
- The production URL embeds the model in the path
  (`https://inference.cloud.celeris.ai/<model>/v1`) and the body `model` field
  must match the path segment.
- Auth: `Authorization: Bearer ck_...` only. Env `CELERIS_API_KEY`, with
  OpenAI-style fallbacks `OPENAI_API_KEY` / `OPENAI_BASE_URL` accepted for
  compatibility. Errors arrive as `{"error":{"message","type","code"}}`; 429
  carries `Retry-After`.
- Production tenants run a low-RPS profile (canary uses ~1 req/1.1s), so the
  e2e test paces requests.

## Command tree (OpenAI-CLI-compatible resource style)

```
celeris [resource] <command> [flags]

celeris chat:completions create   --model -m, --message/-g (role:content, repeatable),
                                  --system, --input/-i (user message; @file / '-' for stdin),
                                  --stream (default true on TTY-less pipes? no — default false,
                                  see Streaming), --max-tokens, --temperature, --top-p, --stop,
                                  --seed, --user
celeris completions create        --model -m, --prompt/-p (@file / '-' / stdin), --stream,
                                  --max-tokens, --temperature, --top-p, --stop, --seed
celeris models list
celeris api <method> <path>       escape hatch: raw authenticated request with JSON body
                                  from stdin/@file (parity with divergence safety valve)
celeris version                   prints version, commit, platform
celeris completion <shell>        shell completions (cobra built-in)
```

Ergonomic alias (divergence from openai-cli, justified by the shell-pipeline
focus): a bare `celeris -i "prompt"` / `echo text | celeris` invocation is NOT
added at the root; instead `celeris q "prompt"` is a thin alias for
`chat:completions create --stream --format text` for pipeline use. Rationale:
keeps root command namespace clean for resources, still one short word.
`q` reads stdin when piped and treats positional args as the user message; if
both are present, stdin becomes context appended to the message (this is the
pattern the cookbook demonstrates).

## Flags and I/O conventions (matching openai-cli)

- Global: `--api-key`, `--base-url`, `--format` (`auto|json|jsonl|pretty|raw|text`),
  `--debug` (dump request/response incl. User-Agent to stderr), `--version`,
  `--timeout`.
- `@file` syntax for file-valued arguments; `-` or piped stdin for input.
- `auto` format: `text` when stdout is a pipe, `pretty` (indented JSON with
  content rendered) on a TTY for full-response commands; `models list` renders
  a table on TTY, JSONL when piped.
- All diagnostics/errors to stderr; exit codes: 0 ok, 1 API/auth error,
  2 usage error. API errors print the server's error message verbatim.
- Streaming writes token deltas to stdout as they arrive; `--format jsonl`
  emits raw SSE chunk JSON per line.

## Architecture

```
cmd/celeris/main.go        thin main
internal/cli/              cobra command tree, flag parsing, I/O plumbing
internal/api/              typed client: ChatCompletions, Completions, Models,
                           Raw; SSE stream decoding; error mapping
internal/version/          Version/Commit/Date vars set via -ldflags
```

- No dependency on the OpenAI Go SDK: the API surface is small and a
  hand-rolled client keeps the binary lean and the User-Agent fully ours.
- `User-Agent: celeris-cli/<version> (<GOOS>; <GOARCH>) go/<goversion>`.
  Version injected by goreleaser ldflags; falls back to module build info
  (`vcs.revision`) then `dev`.
- SSE decoding: incremental scanner, `data: [DONE]` terminator, flushes stdout
  per delta for latency.
- Config precedence: flag > CELERIS_* env > OPENAI_* env. No config file in v1
  (openai-cli is env/flag-driven too; YAGNI).

## Testing

- Unit tests with `httptest`: request shape (auth header, UA format, /v1
  joining), streaming decode, error mapping, format selection, @file/stdin
  handling.
- E2E (in diffusion-llm-service `scheduled_tests.yaml`, new job
  `scheduled-cli-prod` following the `scheduled-docs-prod` shape): checks out
  `ai-celeris/celeris-cli` using `COOKBOOK_SUBMODULE_TOKEN` (fine-grained PAT
  for ai-celeris repos; needs Contents: read on celeris-cli), builds from
  source with the repo Go toolchain, then runs `models list`,
  `chat:completions create` (non-stream + stream), `completions create`, and a
  piped `celeris q` invocation against production Celeris-1 with
  `TEST_PROD_CELERIS_API_KEY`, pacing requests >=1.1s apart, asserting outputs
  non-empty and JSON well-formed. Wired into `failure-pr` and `notify` jobs.

## Release

- goreleaser: darwin/linux (amd64+arm64), windows amd64; tag-driven GitHub
  Actions release workflow; archives + checksums; Homebrew cask/formula pushed
  to `ai-celeris/homebrew-tools` tap => `brew install ai-celeris/tools/celeris`
  (mirrors openai/tools/openai). Tap publish needs a `HOMEBREW_TAP_TOKEN`
  secret (PAT with push to the tap repo) — noted in internal docs.
- CI on PR: gofmt, go vet, go test, build matrix.

## Docs

- External (diffusion-llm-service `components/docs-external/docs/cli.mdx`):
  install + usage page, registered in `sidebars.ts`, explicit `slug` front
  matter, every code fence annotated `docs-e2e=skip` (the docs-snippet e2e
  harness has no CLI binary; the dedicated scheduled CLI job covers live
  coverage instead).
- Internal (diffusion-llm-service `docs/system/`): a design/build/release/
  publishing page for the CLI, picked up by the autogenerated docs-internal
  sidebar.
- Cookbook (`ai-celeris/celeris-cookbook`): `guides/cli-shell-tasks.md`
  registered in `registry.yaml` (+ README/llms.txt lists); must pass style CI
  (no em/en dashes; never mention internal repo names).

## Repos and PRs

- `ai-celeris/celeris-cli` (this repo, empty): initial commit on main with
  README stub, then feature branch + PR with the implementation.
- `marqo-ai/diffusion-llm-service`: one branch/PR carrying the scheduled e2e
  job, external docs page, and internal docs page. PR must call out that
  `COOKBOOK_SUBMODULE_TOKEN` needs Contents: read on `ai-celeris/celeris-cli`
  added, and that the release flow needs `HOMEBREW_TAP_TOKEN` in celeris-cli
  plus an `ai-celeris/homebrew-tools` tap repo.
- `ai-celeris/celeris-cookbook`: one branch/PR with the guide + registry.
