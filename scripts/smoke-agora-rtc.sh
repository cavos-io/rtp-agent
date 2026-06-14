#!/usr/bin/env bash
set -euo pipefail

timeout_seconds="${AGORA_SMOKE_TIMEOUT:-30}"
log_file="${AGORA_SMOKE_LOG:-.tmp/agora-smoke.log}"
binary="${OUT:-.tmp/rtp-agent-agora}"
sdk_dir="${AGORA_GO_SDK_DIR:-}"
runtime_dir="${AGORA_SDK_DATA_DIR:-.tmp/agora-sdk-runtime}"

if [ -z "$sdk_dir" ]; then
  echo "AGORA_GO_SDK_DIR is required." >&2
  exit 1
fi
if [ -z "${AGORA_APP_ID:-}" ]; then
  echo "AGORA_APP_ID is required." >&2
  exit 1
fi
if [ -z "${AGORA_CHANNEL:-}" ]; then
  echo "AGORA_CHANNEL is required." >&2
  exit 1
fi

OUT="$binary" AGORA_GO_SDK_DIR="$sdk_dir" scripts/build-agora-sdk.sh >/dev/null

mkdir -p "$(dirname "$log_file")"
: >"$log_file"
binary_abs="$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")"
log_abs="$(cd "$(dirname "$log_file")" && pwd)/$(basename "$log_file")"
mkdir -p "$runtime_dir"
runtime_dir_abs="$(cd "$runtime_dir" && pwd)"

export RTP_AGENT_TRANSPORT=agora
export LD_LIBRARY_PATH="$sdk_dir/agora_sdk:${LD_LIBRARY_PATH:-}"
export AGORA_SDK_DATA_DIR="$runtime_dir_abs"

(
  cd "$runtime_dir_abs"
  "$binary_abs" start >"$log_abs" 2>&1
) &
pid=$!

cleanup() {
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

deadline=$((SECONDS + timeout_seconds))
while kill -0 "$pid" >/dev/null 2>&1; do
  if grep -q '"msg":"agora transport connected"' "$log_abs"; then
    echo "Agora RTC connected"
    exit 0
  fi
  if grep -q '"msg":"agora transport event error"' "$log_abs"; then
    echo "Agora RTC smoke failed with SDK event error:" >&2
    tail -n 40 "$log_abs" >&2
    exit 1
  fi
  if grep -q 'Worker error:' "$log_abs"; then
    echo "Agora RTC smoke failed with worker error:" >&2
    tail -n 40 "$log_abs" >&2
    exit 1
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "Timed out waiting for Agora RTC connected event:" >&2
    tail -n 40 "$log_abs" >&2
    exit 1
  fi
  sleep 1
done

echo "Agora RTC worker exited before connected event:" >&2
tail -n 40 "$log_abs" >&2
exit 1
