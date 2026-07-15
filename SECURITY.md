# Security Policy

## Reporting a vulnerability

Please do not open a public GitHub issue for security vulnerabilities.

Report suspected vulnerabilities privately to **security@tallyfy.com**. Include:

- a description of the issue and its impact,
- steps to reproduce or a proof of concept,
- the `tallyfy version` output and your OS and architecture.

We acknowledge reports within two business days and will keep you updated as we investigate. Tallyfy's general security and compliance posture is documented at <https://tallyfy.com/legal/>.

## Supported versions

Security fixes land in the latest minor release. Please upgrade before reporting an issue you found on an older build:

```sh
tallyfy version
tallyfy update
```

## How the CLI handles credentials

- API tokens are stored in your operating system keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux) under the service name `com.tallyfy.cli`.
- On machines without a keychain (for example, a headless Linux box with no libsecret or D-Bus), the CLI falls back to an AES-256-GCM encrypted file at `~/.tallyfy/credentials.enc`, with its key stored `0600` alongside. This protects against a casual read of the file, not against an attacker who already controls your account. Prefer a real keychain, or supply the token through `TALLYFY_API_TOKEN` or an `apiKeyHelper` script backed by a secret manager.
- Tokens are never written to a settings file, and they are redacted in `--verbose` output and telemetry.

## Code execution from configuration

`apiKeyHelper` scripts and hook commands execute code. To prevent a cloned repository from running attacker-supplied scripts, the CLI honors them only from trusted scopes (user, local, and managed settings). A committed project `.tallyfy/settings.json` cannot execute a helper or a hook until you explicitly run `tallyfy trust` for that workspace. HTTP hooks must additionally pass a configured host allowlist, which is re-checked on every redirect hop.

## Unsigned binaries

Release binaries are not yet code-signed (macOS) or Authenticode-signed (Windows). Verify downloads against the published `checksums.txt` before running them. See [docs/install.md](docs/install.md) for details.
