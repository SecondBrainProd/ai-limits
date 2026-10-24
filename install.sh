#!/bin/sh
# AI Limits helper installer (macOS, Linux).
#   curl -fsSL https://raw.githubusercontent.com/SecondBrainProd/ai-limits/main/install.sh | sh
# Installs `ailimits` into ~/.local/bin (override with AILIMITS_BIN=/some/dir).
set -eu

BASE="${AILIMITS_BASE_URL:-https://github.com/SecondBrainProd/ai-limits/releases/latest/download}"
BIN="${AILIMITS_BIN:-$HOME/.local/bin}"
REPO="https://github.com/SecondBrainProd/ai-limits/releases/latest"

raw_os=$(uname -s); raw_arch=$(uname -m)
unsupported() {
    echo "Unsupported platform: $raw_os/$raw_arch. Download a release manually: $REPO" >&2
    exit 1
}
case "$raw_os" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) unsupported ;;
esac
case "$raw_arch" in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) unsupported ;;
esac

fetch() { # url dest
    if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
    else echo "Need curl or wget to download AI Limits." >&2; exit 1
    fi
}
sha256() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
    else sha256sum "$1" | cut -d' ' -f1
    fi
}

name="ailimits_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fetch "$BASE/$name" "$tmp/$name"
fetch "$BASE/SHA256SUMS" "$tmp/SHA256SUMS"
expected=$(grep " $name\$" "$tmp/SHA256SUMS" | cut -d' ' -f1 || true)
if [ -z "$expected" ] || [ "$expected" != "$(sha256 "$tmp/$name")" ]; then
    echo "Checksum mismatch for $name — not installing." >&2
    exit 1
fi
tar -xzf "$tmp/$name" -C "$tmp"
mkdir -p "$BIN"
upgrade=no
[ -e "$BIN/ailimits" ] && upgrade=yes
# Copy next to the target, then rename: replacing a running binary in place fails
# on Linux ("text file busy") and can break a signed binary on macOS.
cp "$tmp/ailimits" "$BIN/.ailimits.$$"
chmod 0755 "$BIN/.ailimits.$$"
mv -f "$BIN/.ailimits.$$" "$BIN/ailimits"

echo "Installed $("$BIN/ailimits" version) to $BIN"
cmd=ailimits
case ":$PATH:" in
    *":$BIN:"*) ;;
    *)
        echo "Note: $BIN is not in your PATH. Add it, e.g.: export PATH=\"$BIN:\$PATH\""
        cmd="$BIN/ailimits"
        ;;
esac
if [ "$upgrade" = yes ]; then
    echo "Upgraded. If the service is installed, run $cmd install-service again to restart it."
    exit 0
fi
cat <<NEXT

Next:
  1. Open AI Limits on your watch — it shows a pairing code.
  2. $cmd pair CODE
  3. $cmd install-service      (keeps your limits updating)
Check any time: $cmd status
NEXT
