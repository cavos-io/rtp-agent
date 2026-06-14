#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/agora-smoke-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

mkdir -p "$WORKDIR/scripts" "$WORKDIR/sdk/agora_sdk"
cp "$ROOT/scripts/smoke-agora-rtc.sh" "$WORKDIR/scripts/smoke-agora-rtc.sh"

grep -q '^has_sdk_event_error() {' "$WORKDIR/scripts/smoke-agora-rtc.sh"

cat > "$WORKDIR/scripts/build-agora-sdk.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

binary="${OUT:-.tmp/rtp-agent-agora}"
for var_name in GOMODCACHE GOCACHE GOTMPDIR; do
  var_value="${!var_name:-}"
  if [ -z "$var_value" ]; then
    echo "$var_name is required" >&2
    exit 1
  fi
  if [ ! -d "$var_value" ]; then
    echo "$var_name directory does not exist: $var_value" >&2
    exit 1
  fi
done
if [ "$GOMODCACHE" != "$PWD/.tmp/gomodcache" ]; then
  echo "GOMODCACHE = $GOMODCACHE, want $PWD/.tmp/gomodcache" >&2
  exit 1
fi
if [ "$GOCACHE" != "$PWD/.tmp/gocache" ]; then
  echo "GOCACHE = $GOCACHE, want $PWD/.tmp/gocache" >&2
  exit 1
fi
if [ "$GOTMPDIR" != "$PWD/.tmp/gotmp" ]; then
  echo "GOTMPDIR = $GOTMPDIR, want $PWD/.tmp/gotmp" >&2
  exit 1
fi
mkdir -p "$(dirname "$binary")"
cat > "$binary" <<'BIN'
#!/usr/bin/env bash
case "${FAKE_AGORA_WORKER_MODE:-worker-error}" in
  connected)
    echo '{"msg":"agora transport connected","channel":"support","reason":0}'
    sleep 2
    ;;
  connected-then-worker-error)
    echo '{"msg":"agora transport connected","channel":"support","reason":0}'
    sleep 1
    echo '{"msg":"Worker error","error":"agent session failed after connect"}'
    ;;
  worker-error)
    echo '{"msg":"Worker error","error":"agora SDK connect timed out after 3s"}'
    ;;
  sdk-event-error)
    echo '{"msg":"agora transport event error","channel":"support","reason":110}'
    ;;
esac
BIN
chmod +x "$binary"
SH
chmod +x "$WORKDIR/scripts/build-agora-sdk.sh"

run_smoke() {
  cd "$WORKDIR"
  env -u GOMODCACHE -u GOCACHE -u GOTMPDIR \
    AGORA_GO_SDK_DIR="$WORKDIR/sdk" \
    AGORA_APP_ID="app" \
    AGORA_CHANNEL="support" \
    AGORA_SMOKE_TIMEOUT=5 \
    AGORA_SMOKE_STABLE_SECONDS=1 \
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

if FAKE_AGORA_WORKER_MODE=connected-then-worker-error run_smoke >"$WORKDIR/out-connected-then-worker-error.txt" 2>"$WORKDIR/err-connected-then-worker-error.txt"; then
  echo "smoke script unexpectedly passed after connected log followed by worker error" >&2
  exit 1
fi

grep -q '^Agora RTC smoke failed with worker error:$' "$WORKDIR/err-connected-then-worker-error.txt"
grep -q '"msg":"Worker error"' "$WORKDIR/err-connected-then-worker-error.txt"

if FAKE_AGORA_WORKER_MODE=sdk-event-error run_smoke >"$WORKDIR/out-sdk-event-error.txt" 2>"$WORKDIR/err-sdk-event-error.txt"; then
  echo "smoke script unexpectedly passed after SDK event error" >&2
  exit 1
fi

grep -q '^Agora RTC smoke failed with SDK event error:$' "$WORKDIR/err-sdk-event-error.txt"
grep -q '"msg":"agora transport event error"' "$WORKDIR/err-sdk-event-error.txt"
