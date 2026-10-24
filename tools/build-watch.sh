#!/bin/bash
# Build the watch widget.
#   tools/build-watch.sh <device|all>     → watch/bin/AILimits-<device>.prg
#   tools/build-watch.sh all -e           → also watch/bin/AILimits.iq (Connect IQ Store package)
#   tools/build-watch.sh <device> -t      → build unit tests and run them in the simulator
set -euo pipefail
cd "$(dirname "$0")/../watch"
TARGET="${1:?device id or all}"; MODE="${2:-}"
export JAVA_HOME=/opt/homebrew/opt/openjdk@21 PATH="/opt/homebrew/opt/openjdk@21/bin:$PATH"
SDK="$(cat "$HOME/Library/Application Support/Garmin/ConnectIQ/current-sdk.cfg")"
KEY="$HOME/.config/garmin/developer_key.der"
mkdir -p bin
devices() { grep -oE '<iq:product id="[^"]+"' manifest.xml | cut -d'"' -f2; }
if [ "$MODE" = "-t" ]; then
    "$SDK/bin/monkeyc" -f monkey.jungle -d "$TARGET" -y "$KEY" -o bin/test.prg -l 2 --unit-test
    pgrep -f "ConnectIQ.app/Contents/MacOS/[s]imulator" >/dev/null || { "$SDK/bin/connectiq" >/dev/null 2>&1 & sleep 5; }
    # monkeydo -t exits 0 even when tests fail — decide by the summary line.
    out=$("$SDK/bin/monkeydo" bin/test.prg "$TARGET" -t 2>&1) || true
    echo "$out"
    echo "$out" | grep -qE "^PASSED \(" || { echo "UNIT TESTS FAILED"; exit 1; }
    exit 0
fi
list=$([ "$TARGET" = all ] && devices || echo "$TARGET")
for d in $list; do
    rm -f "bin/AILimits-$d.prg"          # never count a stale build as success
    "$SDK/bin/monkeyc" -f monkey.jungle -d "$d" -y "$KEY" -o "bin/AILimits-$d.prg" -l 2 -w 2>&1 \
        | { grep -vE "launcher icon .* scaled" || true; }
    [ "${PIPESTATUS[0]}" -eq 0 ] && [ -f "bin/AILimits-$d.prg" ] || { echo "FAILED: $d"; exit 1; }
    echo "built: $d"
done
if [ "$MODE" = "-e" ]; then
    "$SDK/bin/monkeyc" -f monkey.jungle -y "$KEY" -o bin/AILimits.iq -e -l 2 -r
    echo "package: watch/bin/AILimits.iq"
fi
