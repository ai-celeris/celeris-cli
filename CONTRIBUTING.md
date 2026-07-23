# Contributing

Thanks for helping improve the Celeris CLI.

## Getting started

```sh
git clone https://github.com/ai-celeris/celeris-cli
cd celeris-cli
go build ./cmd/celeris
go test ./...
```

Go 1.23 or newer is required — the same version the `go.mod` directive names.

## Before you open a pull request

CI runs these; running them locally first is faster than waiting on a red build:

```sh
gofmt -l .        # must print nothing
go vet ./...
go test -race ./...
```

## Conventions

- **Keep changes focused.** One concern per pull request.
- **Comment the why, not the what.** The existing code explains non-obvious
  constraints (why streams get no client timeout, why sampling flags are
  pointers). Match that.
- **Test behavior, not implementation.** Command-level tests drive the real
  cobra tree against an `httptest` server; prefer that over mocking internals.
- **Diagnostics go to stderr, results to stdout.** The CLI is built for
  pipelines, so nothing may pollute stdout that a consumer would have to strip.
- **Exit codes are part of the contract**: `0` success, `1` request/API
  failure, `2` usage error. Return `usageErrorf(...)` for bad invocations.

## Adding a command

Commands live in `internal/cli` and follow the openai-cli resource style
(`celeris <resource> <verb>`). Wire new commands into `NewRootCommand` in
`internal/cli/root.go`. Transport belongs in `internal/api` — keep HTTP details
out of the command layer.

## Releases

Maintainers only. Releases are tag-driven: pushing `vX.Y.Z` runs goreleaser,
which publishes archives and updates the Homebrew cask in
`ai-celeris/homebrew-tools`.

## Security issues

Please do not file them as issues — see [SECURITY.md](SECURITY.md).
