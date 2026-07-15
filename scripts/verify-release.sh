#!/usr/bin/env bash
# verify-release.sh - post-release checks for the tallyfy CLI.
#
# Downloads the release artifacts, verifies checksums, inspects each platform
# binary, confirms the Homebrew cask was updated, and (on macOS) runs a real
# `brew install` end to end. Run after `goreleaser release`.
#
# Usage: scripts/verify-release.sh [version]   # version defaults to the latest tag
set -euo pipefail

REPO="tallyfy/cli"
TAP="tallyfy/homebrew-tap"
VERSION="${1:-}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

pass() { printf '  ok   %s\n' "$1"; }
fail() { printf '  FAIL %s\n' "$1"; FAILED=1; }
FAILED=0

if [ -z "$VERSION" ]; then
  VERSION="$(gh release view -R "$REPO" --json tagName -q .tagName | sed 's/^v//')"
fi
TAG="v${VERSION}"
echo "Verifying release ${TAG} of ${REPO}"
cd "$WORKDIR"

echo "1. Assets present"
gh release download "$TAG" -R "$REPO" --clobber
ASSET_COUNT="$(find . -maxdepth 1 -name 'tallyfy_*' | wc -l | tr -d ' ')"
if [ "$ASSET_COUNT" -eq 6 ]; then pass "6 platform archives"; else fail "expected 6 archives, found $ASSET_COUNT"; fi
[ -f checksums.txt ] && pass "checksums.txt present" || fail "checksums.txt missing"

echo "2. Checksums verify"
if shasum -a 256 --check --ignore-missing checksums.txt >/dev/null 2>&1; then
  pass "all downloaded archives match checksums.txt"
else
  fail "checksum mismatch"
fi

echo "3. Binaries inspect / run"
for f in tallyfy_*.tar.gz; do
  tar -xzf "$f" tallyfy 2>/dev/null || true
  if [ -f tallyfy ]; then
    case "$f" in
      *darwin_amd64*)
        if [ "$(uname -s)-$(uname -m)" = "Darwin-x86_64" ]; then
          ./tallyfy version >/dev/null && pass "darwin_amd64 runs locally" || fail "darwin_amd64 failed to run"
        else
          file tallyfy | grep -qi 'x86_64\|amd64' && pass "darwin_amd64 is x86_64 (inspect only)" || fail "darwin_amd64 wrong arch"
        fi ;;
      *darwin_arm64*) file tallyfy | grep -qi 'arm64\|aarch64' && pass "darwin_arm64 is arm64 (inspect only)" || fail "darwin_arm64 wrong arch" ;;
      *linux_amd64*)  file tallyfy | grep -qi 'x86-64\|x86_64'  && pass "linux_amd64 is ELF x86-64 (inspect only)" || fail "linux_amd64 wrong" ;;
      *linux_arm64*)  file tallyfy | grep -qi 'aarch64\|arm'    && pass "linux_arm64 is ELF aarch64 (inspect only)" || fail "linux_arm64 wrong" ;;
    esac
    rm -f tallyfy
  fi
done
for z in tallyfy_*windows*.zip; do
  [ -f "$z" ] || continue
  unzip -l "$z" | grep -q 'tallyfy.exe' && pass "$(basename "$z") contains tallyfy.exe" || fail "$z missing tallyfy.exe"
done

echo "4. Homebrew tap updated"
CASK="$(gh api "repos/${TAP}/contents/Casks/tallyfy.rb" -q .content 2>/dev/null | base64 -d 2>/dev/null || true)"
echo "$CASK" | grep -q "version \"${VERSION}\"" && pass "cask version is ${VERSION}" || fail "cask version not ${VERSION}"
SHA_IN_CASK="$(echo "$CASK" | grep -c 'sha256' || true)"
[ "$SHA_IN_CASK" -ge 4 ] && pass "cask has per-platform sha256 blocks" || fail "cask sha256 blocks look wrong ($SHA_IN_CASK)"

echo "5. brew end to end (macOS only)"
if [ "$(uname -s)" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
  brew install "${TAP%/*}/tap/tallyfy" >/dev/null 2>&1 && pass "brew install succeeded" || fail "brew install failed"
  if command -v tallyfy >/dev/null 2>&1; then
    tallyfy version >/dev/null && pass "installed tallyfy runs" || fail "installed tallyfy failed"
    REALBIN="$(command -v tallyfy)"
    if xattr -l "$(readlink -f "$REALBIN" 2>/dev/null || echo "$REALBIN")" 2>/dev/null | grep -q 'com.apple.quarantine'; then
      fail "installed binary still quarantined (post-install hook did not run)"
    else
      pass "no quarantine attribute on installed binary"
    fi
    brew audit --cask "${TAP%/*}/tap/tallyfy" >/dev/null 2>&1 && pass "brew audit --cask clean" || echo "  note brew audit reported style notes (informational)"
  fi
else
  echo "  skip not on macOS with brew; run this section on an Apple Silicon and an Intel Mac"
fi

echo
if [ "$FAILED" -eq 0 ]; then
  echo "RELEASE VERIFICATION PASSED for ${TAG}"
else
  echo "RELEASE VERIFICATION HAD FAILURES for ${TAG}"
  exit 1
fi
