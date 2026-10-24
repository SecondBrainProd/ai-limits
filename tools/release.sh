#!/bin/bash
# Build release archives of the helper.
#   tools/release.sh 0.1.0  → dist/: 5 archives (versioned + "latest" names), SHA256SUMS, ailimits.rb
# macOS signing runs only when AILIMITS_SIGN_ID ("Developer ID Application: …") is set;
# notarization additionally needs ASC_KEY_ID, ASC_ISSUER and ASC_KEY_PATH (.p8).
set -euo pipefail
V="${1:?version, e.g. 0.1.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
OUT="$ROOT/dist"; rm -rf "$OUT"; mkdir -p "$OUT"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64"

sign_darwin() { # binary
    if [ -z "${AILIMITS_SIGN_ID:-}" ]; then echo "  unsigned (AILIMITS_SIGN_ID not set)"; return; fi
    codesign --force --options runtime --timestamp -s "$AILIMITS_SIGN_ID" "$1"
    if [ -n "${ASC_KEY_ID:-}" ] && [ -n "${ASC_ISSUER:-}" ] && [ -n "${ASC_KEY_PATH:-}" ]; then
        ditto -c -k "$1" "$WORK/notarize.zip"
        xcrun notarytool submit "$WORK/notarize.zip" --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER" --wait
    else
        echo "  signed, not notarized (ASC_* not set)"
    fi
}

for t in $TARGETS; do
    os=${t%/*}; arch=${t#*/}
    d="$WORK/$os-$arch"; mkdir -p "$d"
    exe=ailimits; [ "$os" = windows ] && exe=ailimits.exe
    echo "build $os/$arch"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$V" -o "$d/$exe" ./helper/cmd/ailimits
    [ "$os" = darwin ] && sign_darwin "$d/$exe"
    cp LICENSE README.md "$d/" 2>/dev/null || cp LICENSE "$d/"
    if [ "$os" = windows ]; then
        (cd "$d" && zip -q -X "$OUT/ailimits_${V}_${os}_${arch}.zip" ./*)
        cp "$OUT/ailimits_${V}_${os}_${arch}.zip" "$OUT/ailimits_${os}_${arch}.zip"
    else
        tar -czf "$OUT/ailimits_${V}_${os}_${arch}.tar.gz" -C "$d" .
        cp "$OUT/ailimits_${V}_${os}_${arch}.tar.gz" "$OUT/ailimits_${os}_${arch}.tar.gz"
    fi
done
# install.sh extracts "ailimits" at the archive root
(cd "$OUT" && shasum -a 256 ailimits_* > SHA256SUMS)

f="$(cat packaging/homebrew/ailimits.rb.tmpl)"; f="${f//\{\{VERSION\}\}/$V}"
for t in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
    sum=$(grep " ailimits_${V}_${t}.tar.gz\$" "$OUT/SHA256SUMS" | cut -d' ' -f1)
    f="${f//\{\{SHA_$t\}\}/$sum}"
done
printf '%s\n' "$f" > "$OUT/ailimits.rb"
echo "dist: $(ls "$OUT" | wc -l | tr -d ' ') files"
