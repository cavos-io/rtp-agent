#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/agora-smoke-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

mkdir -p "$WORKDIR/scripts" "$WORKDIR/sdk/agora_sdk"
cp "$ROOT/scripts/smoke-agora-rtc.sh" "$WORKDIR/scripts/smoke-agora-rtc.sh"

cat > "$WORKDIR/scripts/build-agora-sdk.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

binary="${OUT:-.tmp/rtp-agent-agora}"
mkdir -p "$(dirname "$binary")"
cat > "$binary" <<'BIN'
#!/usr/bin/env bash
echo 'Worker error: agora SDK error 110: denied'
sleep 5
BIN
chmod +x "$binary"
SH
chmod +x "$WORKDIR/scripts/build-agora-sdk.sh"

if (
  cd "$WORKDIR"
  AGORA_GO_SDK_DIR="$WORKDIR/sdk" \
    AGORA_APP_ID="app" \
    AGORA_CHANNEL="support" \
    AGORA_SMOKE_TIMEOUT=5 \
    scripts/smoke-agora-rtc.sh
) >"$WORKDIR/out.txt" 2>"$WORKDIR/err.txt"; then
  echo "smoke script unexpectedly passed after worker error" >&2
  exit 1
fi

grep -q '^Agora RTC smoke failed with worker error:$' "$WORKDIR/err.txt"
grep -q 'Worker error: agora SDK error 110: denied' "$WORKDIR/err.txt"
