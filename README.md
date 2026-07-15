# Tallyfy CLI

Deterministic, scriptable Tallyfy workflow automation for your terminal.

[![ci](https://github.com/tallyfy/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/tallyfy/cli/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/tallyfy/cli?sort=semver)](https://github.com/tallyfy/cli/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/tallyfy/cli.svg)](https://pkg.go.dev/github.com/tallyfy/cli)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

`tallyfy` is the official command-line interface for [Tallyfy](https://tallyfy.com). It launches processes, completes tasks, exports and imports blueprints, and gates CI/CD pipelines on human approvals. One static binary, real exit codes, no browser and no AI client in the loop.

Tallyfy already has a web UI (for people), a REST API (for embedding), and an [MCP server](https://tallyfy.com/products/pro/integrations/mcp-server/) (for AI assistants). The CLI is the fourth surface: the one built for repeatable, headless automation. Same engine underneath, a different job.

> **Vocabulary.** Tallyfy's UI says *blueprint* and *process*; the API says *checklist* and *run*. This CLI leads with the UI words and accepts the API words as aliases (`tallyfy checklist list` works too).

## Install

### Homebrew (macOS and Linux)

```sh
brew install tallyfy/tap/tallyfy
```

### Direct download

Grab a binary from [Releases](https://github.com/tallyfy/cli/releases), verify the checksum, and put it on your `PATH`:

```sh
curl -fsSLO https://github.com/tallyfy/cli/releases/latest/download/tallyfy_0.1.0_darwin_arm64.tar.gz
curl -fsSLO https://github.com/tallyfy/cli/releases/latest/download/checksums.txt
shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf tallyfy_0.1.0_darwin_arm64.tar.gz tallyfy
sudo mv tallyfy /usr/local/bin/
```

### Go install

```sh
go install github.com/tallyfy/cli/cmd/tallyfy@latest
```

### Windows

Download the `windows` zip from [Releases](https://github.com/tallyfy/cli/releases), unzip it, then unblock the binary:

```powershell
Unblock-File .\tallyfy.exe
```

Binaries are not yet code-signed or notarized. See [docs/install.md](docs/install.md) for the Gatekeeper and SmartScreen details.

## Quick start

```sh
# 1. Sign in: opens your browser to the token page, then paste the token.
tallyfy login

# 2. Pick an organization (multi-org accounts).
tallyfy org list
tallyfy org use org_abc123

# 3. Read.
tallyfy blueprint list
tallyfy blueprint get bp_123 --output json | jq '.data.title'

# 4. Act.
tallyfy process launch --blueprint "Employee Onboarding" --name "Jo Smith"
tallyfy task list --process run_456
```

## What the CLI is for

- **Workflows as code.** Export a blueprint to JSON, commit it, review changes in a pull request, and promote the same definition from a sandbox org to production.
  ```sh
  tallyfy blueprint export bp_123 > blueprints/onboarding.json
  tallyfy blueprint import blueprints/onboarding.json --org org_prod
  ```
- **Bulk operations.** Launch one process per CSV row, with a dry run first and a clean exit code when some rows fail.
  ```sh
  tallyfy process launch --blueprint "Onboarding" --from-csv new-hires.csv --dry-run
  ```
- **CI/CD approval gates.** Block a deploy until a human approves a task.
  ```sh
  tallyfy task wait --process "$RUN_ID" --task "VP sign-off" --timeout 2h || exit 1
  ```
- **Multi-org management.** Run one script across every organization you belong to.
  ```sh
  for org in $(tallyfy org list --json | jq -r '.[].id'); do
    tallyfy blueprint import standard-sop.json --org "$org"
  done
  ```

## Using it in CI

Set a token as an environment variable and run headless. The CLI never prompts when there is no TTY.

```sh
export TALLYFY_API_TOKEN="$TALLYFY_TOKEN"   # a secret in your CI settings
tallyfy process launch --blueprint bp_123 --name "Release $GIT_TAG" --no-input --yes
```

Prefer an **application token** over a personal token for CI. Personal tokens are invalidated when you log out of the Tallyfy web app.

### Exit codes

Every command returns a stable exit code, so pipelines can branch on the outcome:

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Generic error |
| 2 | Usage or bad flags |
| 3 | Authentication error |
| 4 | Blocked by a permission rule |
| 5 | Not found |
| 6 | Rate-limited after retries |
| 7 | Validation error |
| 8 | Blocked by a hook |
| 9 | Partial failure in a bulk operation |

## Configuration and safety

Settings live in layered JSON files with strict precedence (managed policy > flags > local > project > user > built-in defaults). Scalar keys override by precedence; permission rules, hooks, and MCP server lists merge across scopes.

```jsonc
// ~/.tallyfy/settings.json
{
  "$schema": "https://cli.tallyfy.com/schemas/settings.json",
  "org": "org_abc123",
  "output": "table",
  "permissions": {
    "defaultMode": "ask",
    "allow": ["Blueprint(list)", "Process(list)", "Task(list)"],
    "deny":  ["Blueprint(delete)", "*(delete)"]
  }
}
```

The permission engine gates every command with `Resource(verb)` rules. Deny always wins, and an organization can ship non-overridable managed policy. When you are stuck:

```sh
tallyfy doctor                       # validate config, auth, connectivity, rules
tallyfy config list --show-sources   # see which file supplied each setting
```

Credentials are stored in your OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux), with an encrypted-file fallback for headless machines. Tokens never land in a settings file.

## Documentation

- Product docs: <https://tallyfy.com/products/pro/integrations/cli/>
- Which surface to use (CLI vs API vs MCP): <https://tallyfy.com/products/pro/integrations/cli/cli-vs-api-vs-mcp/>
- REST API: <https://tallyfy.com/products/pro/integrations/open-api/>
- Per-command help: `tallyfy <command> --help`

## Versioning and support

`tallyfy` follows [Semantic Versioning](https://semver.org). Check for and install updates with:

```sh
tallyfy update --check
tallyfy update
```

(If you installed via Homebrew, use `brew upgrade tallyfy/tap/tallyfy` instead.)

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and conventions, and [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

[Apache-2.0](LICENSE). Copyright Tallyfy, Inc.
