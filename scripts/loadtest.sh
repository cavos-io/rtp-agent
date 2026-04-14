#!/usr/bin/env bash
# =============================================================================
# scripts/loadtest.sh — rtp-agent load test
# Simulasi N rooms dengan auto-dispatch ke rtp-agent langsung
# Usage: ./scripts/loadtest.sh [rooms] [duration]
# =============================================================================
set -euo pipefail

ROOMS=${1:-10}
DURATION=${2:-5m}
ECHO_DELAY=${ECHO_DELAY:-5s}

# Load .env
if [ -f .env ]; then
  export $(grep -v '^#' .env | xargs)
fi

LIVEKIT_URL=${LIVEKIT_URL:?"LIVEKIT_URL tidak di-set"}
LIVEKIT_API_KEY=${LIVEKIT_API_KEY:?"LIVEKIT_API_KEY tidak di-set"}
LIVEKIT_API_SECRET=${LIVEKIT_API_SECRET:?"LIVEKIT_API_SECRET tidak di-set"}
AGENT_NAME=${AGENT_NAME:-cavos-voice-agent}

# Cek lk CLI
if ! command -v lk &> /dev/null; then
  LK_BIN="$HOME/go/bin/lk"
  if [ ! -f "$LK_BIN" ]; then
    echo "[ERROR] lk CLI tidak ditemukan."
    echo "Download: https://github.com/livekit/livekit-cli/releases"
    exit 1
  fi
else
  LK_BIN="lk"
fi

echo "========================================"
echo "  rtp-agent Load Test"
echo "========================================"
echo "  URL        : $LIVEKIT_URL"
echo "  Agent      : $AGENT_NAME"
echo "  Rooms      : $ROOMS"
echo "  Duration   : $DURATION"
echo "  Echo delay : $ECHO_DELAY"
echo "  pprof      : http://localhost:6060/debug/pprof/"
echo "========================================"
echo ""
echo "[INFO] Pastikan rtp-agent sudah berjalan: go run ./cmd/main.go start"
echo "[INFO] Monitor resource: ./scripts/check-resources.sh"
echo ""
read -p "Lanjutkan? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  echo "Dibatalkan."
  exit 0
fi

echo ""
echo "[START] $(date '+%Y-%m-%d %H:%M:%S') — Load test $ROOMS rooms selama $DURATION..."
echo ""

"$LK_BIN" perf agent-load-test \
  --url "$LIVEKIT_URL" \
  --api-key "$LIVEKIT_API_KEY" \
  --api-secret "$LIVEKIT_API_SECRET" \
  --agent-name "$AGENT_NAME" \
  --rooms "$ROOMS" \
  --duration "$DURATION" \
  --echo-speech-delay "$ECHO_DELAY"

echo ""
echo "[DONE] $(date '+%Y-%m-%d %H:%M:%S') — Load test selesai."


lk perf agent-load-test ` --url wss://first-test-smn9006t.livekit.cloud ` --api-key APIbNwMFHLB4QtC ` --api-secret ofPQ1UiLQ5Nf87lMX3pXrLyf87sBCz2iTMZ5eACocdoB ` --agent-name cavos-voice-agent ` --rooms 5 ` --duration 1m ` --echo-speech-delay 5s