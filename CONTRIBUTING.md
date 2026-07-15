# Contributing to the Tallyfy CLI

Thanks for your interest in improving `tallyfy`. This guide covers the local setup and the conventions the project follows.

## Prerequisites

- Go 1.24 or newer
- [golangci-lint](https://golangci-lint.run) v2.12 or newer
- [GoReleaser](https://goreleaser.com) v2 (only needed to test packaging)

## Build and test

```sh
make build     # build ./tallyfy with version metadata
make test      # go test -race ./...
make lint      # golangci-lint run
make snapshot  # goreleaser release --snapshot --clean (local, no publish)
```

Before opening a pull request, make sure all four of these pass:

```sh
gofmt -l .            # prints nothing when formatting is clean
go vet ./...
golangci-lint run
go test -race ./...
```

## Project layout

```
cmd/tallyfy/        entrypoint
internal/cli/       cobra commands, one file per resource
internal/config/    settings scopes, precedence, merge, trust
internal/auth/      credential resolution, keychain, encrypted-file fallback
internal/permissions/  Resource(verb) rule engine (deny wins)
internal/hooks/     lifecycle hooks (exec and http)
internal/output/    table / json / csv / ndjson renderers
internal/update/    self-update from GitHub Releases
pkg/tallyfy/        the Tallyfy REST API client
```

Each command file self-registers through an `init()` that calls `register(...)`, so adding a command does not require editing a central list.

## Conventions

- **Commits** follow [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `docs:`, `chore:`, and so on). Pull requests are squash-merged, so the PR title becomes the commit and must follow the same format. These prefixes drive the generated changelog.
- **Branches** are named `feat/...`, `fix/...`, `chore/...`, or `docs/...`.
- **Generated files are never hand-edited.** The Homebrew cask in `tallyfy/homebrew-tap` and the GitHub release notes are produced by GoReleaser.
- **Keep dependencies lean.** Prefer the standard library. New third-party modules need a clear justification in the pull request.

## Tests

- Unit tests are table-driven and live beside the code they cover.
- End-to-end tests in `test/e2e/` compile the real binary and run it against an in-process mock API server, asserting output (golden files) and exit codes.
- The live staging suite in `test/live/` is gated behind environment variables and never runs in public CI. It creates and deletes its own fixtures and refuses to touch anything not prefixed `cli-e2e-`. See the file header for the required variables.

## Reporting security issues

Do not open a public issue. See [SECURITY.md](SECURITY.md).
