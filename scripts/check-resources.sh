#!/usr/bin/env bash
# =============================================================================
# scripts/check-resources.sh — monitor pprof selama load test
# Usage: ./scripts/check-resources.sh [interval_detik]
# =============================================================================
set -euo pipefail

PPROF_ADDR=${PPROF_ADDR:-localhost:6060}
INTERVAL=${1:-15}
OUTPUT_DIR="loadtest-results/$(date '+%Y%m%d_%H%M%S')"

mkdir -p "$OUTPUT_DIR"

echo "========================================"
echo "  Resource Monitor — rtp-agent"
echo "========================================"
echo "  pprof addr : $PPROF_ADDR"
echo "  Interval   : setiap ${INTERVAL}s"
echo "  Output dir : $OUTPUT_DIR"
echo "========================================"
echo ""
echo "Tekan Ctrl+C untuk berhenti."
echo ""

if ! curl -sf "http://$PPROF_ADDR/debug/pprof/" > /dev/null; then
  echo "[ERROR] Tidak bisa konek ke pprof di http://$PPROF_ADDR"
  echo "Pastikan rtp-agent sudah jalan dengan PPROF_ADDR=$PPROF_ADDR"
  exit 1
fi

echo "[OK] Terhubung ke pprof."
echo ""

SNAPSHOT=0

snapshot() {
  SNAPSHOT=$((SNAPSHOT + 1))
  TS=$(date '+%Y-%m-%d %H:%M:%S')
  SNAP_DIR="$OUTPUT_DIR/snapshot_$(printf '%03d' $SNAPSHOT)"
  mkdir -p "$SNAP_DIR"

  echo "── Snapshot #$SNAPSHOT [$TS] ──────────────────────"

  GOROUTINE_COUNT=$(curl -sf "http://$PPROF_ADDR/debug/pprof/goroutine?debug=1" \
    | grep -E "^goroutine [0-9]+ \[" | wc -l | tr -d ' ')
  echo "  Goroutines aktif : $GOROUTINE_COUNT"

  HEAP_LINE=$(curl -sf "http://$PPROF_ADDR/debug/pprof/heap?debug=1" \
    | grep -E "^# (Alloc|Sys|HeapInuse)" | head -3)
  echo "$HEAP_LINE" | while IFS= read -r line; do echo "  $line"; done

  curl -sf "http://$PPROF_ADDR/debug/pprof/goroutine?debug=2" \
    > "$SNAP_DIR/goroutines.txt" 2>/dev/null && echo "  [saved] goroutines.txt"

  curl -sf "http://$PPROF_ADDR/debug/pprof/heap?debug=1" \
    > "$SNAP_DIR/heap.txt" 2>/dev/null && echo "  [saved] heap.txt"

  if (( SNAPSHOT % 3 == 1 )); then
    echo "  [profiling CPU 5s...]"
    curl -sf "http://$PPROF_ADDR/debug/pprof/profile?seconds=5" \
      > "$SNAP_DIR/cpu.prof" 2>/dev/null && \
      echo "  [saved] cpu.prof (analyze: go tool pprof $SNAP_DIR/cpu.prof)"
  fi

  echo ""
}

while true; do
  snapshot
  sleep "$INTERVAL"
done
