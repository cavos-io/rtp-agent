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
case "${FAKE_AGORA_WORKER_MODE:-worker-error}" in
  connected)
    echo '{"msg":"agora transport connected","channel":"support","reason":0}'
    ;;
  worker-error)
    echo '{"msg":"Worker error","error":"agora SDK connect timed out after 3s"}'
    ;;
esac
BIN
chmod +x "$binary"
SH
chmod +x "$WORKDIR/scripts/build-agora-sdk.sh"

run_smoke() {
  cd "$WORKDIR"
  AGORA_GO_SDK_DIR="$WORKDIR/sdk" \
    AGORA_APP_ID="app" \
    AGORA_CHANNEL="support" \
    AGORA_SMOKE_TIMEOUT=5 \
    scripts/smoke-agora-rtc.sh
}

if run_smoke >"$WORKDIR/out-worker-error.txt" 2>"$WORKDIR/err-worker-error.txt"; then
  echo "smoke script unexpectedly passed after worker error" >&2
  exit 1
fi

grep -q '^Agora RTC smoke failed with worker error:$' "$WORKDIR/err-worker-error.txt"
grep -q '"msg":"Worker error"' "$WORKDIR/err-worker-error.txt"

if ! FAKE_AGORA_WORKER_MODE=connected run_smoke >"$WORKDIR/out-connected.txt" 2>"$WORKDIR/err-connected.txt"; then
  echo "smoke script did not pass after connected log" >&2
  cat "$WORKDIR/err-connected.txt" >&2
  exit 1
fi

grep -q '^Agora RTC connected$' "$WORKDIR/out-connected.txt"
