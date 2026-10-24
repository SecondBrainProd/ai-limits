#!/bin/sh
# Tests install.sh against a local "release" (file:// URLs) with a faked uname.
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
fail=0
ok()  { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }

# fake release: an "ailimits" that prints its version
mkdir -p "$W/rel" "$W/pkg"
printf '#!/bin/sh\necho "ailimits 9.9.9-test"\n' > "$W/pkg/ailimits"; chmod +x "$W/pkg/ailimits"
tar -czf "$W/rel/ailimits_darwin_arm64.tar.gz" -C "$W/pkg" ailimits
(cd "$W/rel" && shasum -a 256 ailimits_darwin_arm64.tar.gz > SHA256SUMS)

fake_uname() { # $1 = os, $2 = arch
    mkdir -p "$W/fake"
    printf '#!/bin/sh\ncase "$1" in -s) echo %s;; -m) echo %s;; esac\n' "$1" "$2" > "$W/fake/uname"
    chmod +x "$W/fake/uname"
}
run() { PATH="$W/fake:$PATH" AILIMITS_BASE_URL="file://$W/rel" AILIMITS_BIN="$1" sh "$ROOT/install.sh" 2>&1; }

fake_uname Darwin arm64
out=$(run "$W/bin1"); code=$?
[ $code -eq 0 ] && [ -x "$W/bin1/ailimits" ] && echo "$out" | grep -q "ailimits pair" && echo "$out" | grep -q "install-service" \
    && ok "darwin/arm64 installs and prints next steps" || bad "darwin/arm64 install ($code): $out"
echo "$out" | grep -q "is not in your PATH" && ok "warns when the bin dir is not in PATH" || bad "no PATH warning: $out"

fake_uname FreeBSD amd64
out=$(run "$W/bin2"); code=$?
[ $code -eq 1 ] && echo "$out" | grep -q "Unsupported platform: FreeBSD/amd64" && [ ! -e "$W/bin2/ailimits" ] \
    && ok "unsupported platform explains and exits 1" || bad "unsupported ($code): $out"

fake_uname Darwin arm64
echo "0000000000000000000000000000000000000000000000000000000000000000  ailimits_darwin_arm64.tar.gz" > "$W/rel/SHA256SUMS"
out=$(run "$W/bin3"); code=$?
[ $code -eq 1 ] && [ ! -e "$W/bin3/ailimits" ] && echo "$out" | grep -q "Checksum mismatch" \
    && ok "bad checksum installs nothing" || bad "checksum ($code): $out"

fake_uname Darwin arm64
(cd "$W/rel" && shasum -a 256 ailimits_darwin_arm64.tar.gz > SHA256SUMS)
mkdir -p "$W/bin4"; printf 'old binary' > "$W/bin4/ailimits"; chmod +x "$W/bin4/ailimits"
out=$(run "$W/bin4"); code=$?
[ $code -eq 0 ] && grep -q "9.9.9-test" "$W/bin4/ailimits" && echo "$out" | grep -q "install-service again" \
    && ok "upgrade replaces the old binary and says to restart the service" || bad "upgrade ($code): $out"
ls "$W/bin4" | grep -q '^\.ailimits' && bad "temporary file left behind" || ok "no temporary file left"
out=$(run "$W/bin5")
echo "$out" | grep -q "$W/bin5/ailimits pair CODE" && ok "next steps use the full path when the dir is not in PATH" || bad "next steps path: $out"

exit $fail
