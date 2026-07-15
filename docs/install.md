# Installing the Tallyfy CLI

The fastest path is Homebrew. Every method below gives you the same `tallyfy` binary.

## Homebrew (macOS and Linux)

```sh
brew install tallyfy/tap/tallyfy
tallyfy version
```

Upgrade with `brew upgrade tallyfy/tap/tallyfy`. Homebrew is the recommended channel because it handles the unsigned-binary quarantine for you (see below).

## Direct download

Binaries for every platform are attached to each [GitHub release](https://github.com/tallyfy/cli/releases) as `tallyfy_<version>_<os>_<arch>.tar.gz` (or `.zip` for Windows), alongside a `checksums.txt`.

```sh
VERSION=0.1.0
OS=darwin      # darwin | linux | windows
ARCH=arm64     # amd64 | arm64
curl -fsSLO "https://github.com/tallyfy/cli/releases/download/v${VERSION}/tallyfy_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/tallyfy/cli/releases/download/v${VERSION}/checksums.txt"
shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf "tallyfy_${VERSION}_${OS}_${ARCH}.tar.gz" tallyfy
sudo mv tallyfy /usr/local/bin/
```

Always verify the checksum before running a downloaded binary.

## Go install

```sh
go install github.com/tallyfy/cli/cmd/tallyfy@latest
```

This builds from source, so it needs a Go toolchain but skips the signing and quarantine questions entirely.

## Shell completions

```sh
tallyfy completion bash   > /usr/local/etc/bash_completion.d/tallyfy   # bash
tallyfy completion zsh    > "${fpath[1]}/_tallyfy"                     # zsh
tallyfy completion fish   > ~/.config/fish/completions/tallyfy.fish    # fish
tallyfy completion powershell | Out-String | Invoke-Expression        # PowerShell
```

## A note on unsigned binaries

The v0.1.0 binaries are not yet code-signed or notarized. That has no effect on Homebrew, `curl`, `wget`, or `go install` downloads. It only matters when a **browser** downloads the archive, because browsers tag downloads with a quarantine or mark-of-the-web attribute that the operating system then blocks.

### macOS ("tallyfy cannot be opened because the developer cannot be verified")

This appears only for browser-downloaded archives. Clear the quarantine attribute:

```sh
xattr -d com.apple.quarantine ./tallyfy
```

Or open **System Settings > Privacy and Security** and choose **Allow Anyway** after the first blocked run. `curl` and `wget` downloads are never quarantined, so the scripted download above avoids this entirely.

### Windows (SmartScreen)

A browser-downloaded `.exe` gets a mark-of-the-web flag that triggers a SmartScreen prompt. Clear it:

```powershell
Unblock-File .\tallyfy.exe
```

Or choose **More info > Run anyway** at the prompt.

Code signing and notarization are on the roadmap. Once they land, this section goes away.

## Verify your install

```sh
tallyfy version
tallyfy doctor
```

`tallyfy doctor` checks your configuration, authentication, connectivity, and permission rules, and tells you what to fix if anything is off.
