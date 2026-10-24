#!/bin/bash
# Screenshot the widget in the Connect IQ simulator (macOS only).
#   tools/sim.sh <device> glance <out.png>   — glance in the widget loop
#   tools/sim.sh <device> main   <out.png>   — full screen (built without the glance)
#   tools/sim.sh <device> pair   <out.png>   — full screen with fresh app storage → pairing code
# Needs: SDK via SDK Manager, openjdk@21, Accessibility + Screen Recording for the terminal.
set -euo pipefail
DEV="$1"; MODE="$2"; OUT="$(cd "$(dirname "$3")" && pwd)/$(basename "$3")"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export JAVA_HOME=/opt/homebrew/opt/openjdk@21 PATH="/opt/homebrew/opt/openjdk@21/bin:$PATH"
SDK="$(cat "$HOME/Library/Application Support/Garmin/ConnectIQ/current-sdk.cfg")"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
cp -R "$ROOT/watch" "$ROOT/tools" "$WORK/"
if [ "$MODE" != glance ]; then
    # Without getGlanceView the simulator opens the full screen directly.
    python3 - "$WORK/watch/source/App.mc" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s = re.sub(r"    \(:glance :typecheck\(disableBackgroundCheck\)\)\n    function getGlanceView\(\).*?\n    }\n", "", s, flags=re.S)
open(p, "w").write(s)
PY
fi
"$WORK/tools/build-watch.sh" "$DEV" >/dev/null
# Pattern trick [M]: plain "MonkeyDoDeux" would match this script's own command line.
pkill -f "[M]onkeyDoDeux" || true
[ "$MODE" = pair ] && rm -rf "${TMPDIR:-/tmp}/com.garmin.connectiq/GARMIN/APPS/DATA/"*
pgrep -f "ConnectIQ.app/Contents/MacOS/[s]imulator" >/dev/null || { "$SDK/bin/connectiq" >/dev/null 2>&1 & sleep 6; }
(cd "$WORK/watch" && nohup "$SDK/bin/monkeydo" "bin/AILimits-$DEV.prg" "$DEV" >/dev/null 2>&1 &)
sleep 14
osascript -e 'tell application "System Events" to tell process "simulator"' \
          -e 'set frontmost to true' -e 'perform action "AXRaise" of window 1' -e 'end tell' >/dev/null
sleep 1
B=$(osascript -e 'tell application "System Events" to tell process "simulator" to get {position, size} of window 1' | tr -d ' ')
IFS=, read -r X Y W H <<< "$B"
# Drop the window title bar (30 px) and the status bar (34 px).
screencapture -x -R"$((X+2)),$((Y+31)),$((W-4)),$((H-65))" "$OUT"
echo "$OUT"
