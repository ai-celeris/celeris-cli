# Security Policy

## Reporting a vulnerability

Please do not open a public issue for security reports.

Report vulnerabilities through
[GitHub private vulnerability reporting](https://github.com/ai-celeris/celeris-cli/security/advisories/new),
or by email to **security@celeris.ai**.

Include the CLI version (`celeris version`), your platform, and the steps to
reproduce. We aim to acknowledge reports within three business days.

## Supported versions

Fixes land on `main` and ship in the next tagged release. Only the latest
release is supported; there are no backport branches.

## Handling of credentials

The CLI reads your API key from `--api-key`, `$CELERIS_API_KEY`, or
`$OPENAI_API_KEY`, and sends it only as an `Authorization: Bearer` header to
the configured endpoint.

- `--debug` traces the method, URL, User-Agent, and request/response bodies to
  stderr. It deliberately **does not** print the `Authorization` header — but
  request bodies contain your prompts, so redirect that output with care.
- Prefer the environment variable over `--api-key`: arguments are visible to
  other processes on the machine via the process list, and are recorded in
  shell history.
- `--base-url` sends your key to whatever host you name. Point it only at
  endpoints you trust.

## Release integrity

Release archives and a `checksums.txt` are published on the
[releases page](https://github.com/ai-celeris/celeris-cli/releases). Verify a
download before running it:

```sh
sha256sum -c checksums.txt --ignore-missing
```

Binaries are **not** currently code-signed or notarized. The Homebrew cask
removes the macOS quarantine attribute on install so Gatekeeper does not block
the first run — see the note in the README. Signed and notarized builds are
tracked as future work; if that matters to you, build from source instead:

```sh
go install github.com/ai-celeris/celeris-cli/cmd/celeris@latest
```
