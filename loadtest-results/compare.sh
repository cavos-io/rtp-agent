#!/usr/bin/env bash
# =============================================================================
# compare.sh — Compare resource usage Go (rtp-agent) vs Python di Docker
# Usage: ./compare.sh <python_container_name> <python_pprof_or_metrics_port>
#
# Cara pakai:
#   ./compare.sh python-agent-1 8080
# =============================================================================

GO_CONTAINER=${GO_CONTAINER:-rtp-agent-agent-1}
PY_CONTAINER=${1:-python-agent-1}
PY_PORT=${2:-8080}
GO_PPROF=${GO_PPROF:-localhost:6065}

echo "============================================================"
echo "  Go vs Python Docker — Resource Comparison"
echo "============================================================"
echo "  Go  container : $GO_CONTAINER (pprof: $GO_PPROF)"
echo "  Py  container : $PY_CONTAINER (metrics: localhost:$PY_PORT)"
echo "============================================================"
echo ""

# ── Go metrics dari pprof ─────────────────────────────────────────────────────
GO_G=$(curl -sf "http://$GO_PPROF/debug/pprof/goroutine?debug=1" | head -1 | grep -oE "[0-9]+")
GO_A=$(curl -sf "http://$GO_PPROF/debug/pprof/heap?debug=1" | grep "^# Alloc" | awk '{print $4}')
GO_H=$(curl -sf "http://$GO_PPROF/debug/pprof/heap?debug=1" | grep "^# HeapInuse" | awk '{print $4}')
GO_STATS=$(docker stats "$GO_CONTAINER" --no-stream --format "{{.MemUsage}}|{{.CPUPerc}}" 2>/dev/null)
GO_MEM=$(echo "$GO_STATS" | cut -d'|' -f1)
GO_CPU=$(echo "$GO_STATS" | cut -d'|' -f2)

# ── Python metrics dari docker stats ─────────────────────────────────────────
PY_STATS=$(docker stats "$PY_CONTAINER" --no-stream --format "{{.MemUsage}}|{{.CPUPerc}}" 2>/dev/null)
PY_MEM=$(echo "$PY_STATS" | cut -d'|' -f1)
PY_CPU=$(echo "$PY_STATS" | cut -d'|' -f2)

# ── Print comparison ──────────────────────────────────────────────────────────
echo "=== CURRENT RESOURCE USAGE ==="
echo ""
printf "%-25s %-20s %-20s\n" "Metric" "Go (rtp-agent)" "Python"
printf "%-25s %-20s %-20s\n" "-------------------------" "--------------------" "--------------------"
printf "%-25s %-20s %-20s\n" "Container Memory"   "$GO_MEM"  "$PY_MEM"
printf "%-25s %-20s %-20s\n" "CPU Usage"          "$GO_CPU"  "$PY_CPU"
printf "%-25s %-20s %-20s\n" "Goroutines/Threads" "$GO_G goroutines" "see py metrics"
printf "%-25s %-20s %-20s\n" "Heap Alloc"         "$(awk "BEGIN{printf \"%.2f MB\", $GO_A/1024/1024}")" "N/A"
printf "%-25s %-20s %-20s\n" "HeapInuse"          "$(awk "BEGIN{printf \"%.2f MB\", $GO_H/1024/1024}")" "N/A"
echo ""

# ── Hasil test dari file metrics ─────────────────────────────────────────────
echo "=== JOIN DELAY COMPARISON (dari hasil test) ==="
echo ""
printf "%-25s %-20s %-20s\n" "Metric" "Go (50 rooms)" "Python (50 rooms)"
printf "%-25s %-20s %-20s\n" "-------------------------" "--------------------" "--------------------"
printf "%-25s %-20s %-20s\n" "Success Rate"  "50/50 (100%)"  "?/50 (?%)"
printf "%-25s %-20s %-20s\n" "Min Delay"     "188ms"         "?"
printf "%-25s %-20s %-20s\n" "Avg Delay"     "318ms"         "?"
printf "%-25s %-20s %-20s\n" "P90 Delay"     "~530ms"        "?"
printf "%-25s %-20s %-20s\n" "Max Delay"     "804ms"         "?"
echo ""
echo "[INFO] Isi kolom Python setelah load test Python selesai."
echo ""

# ── Live monitor loop (opsional) ─────────────────────────────────────────────
if [[ "${MONITOR:-false}" == "true" ]]; then
  echo "=== LIVE MONITOR (Ctrl+C untuk stop) ==="
  while true; do
    TS=$(date '+%H:%M:%S')
    GO_S=$(docker stats "$GO_CONTAINER" --no-stream --format "{{.MemUsage}}|{{.CPUPerc}}" 2>/dev/null)
    PY_S=$(docker stats "$PY_CONTAINER" --no-stream --format "{{.MemUsage}}|{{.CPUPerc}}" 2>/dev/null)
    GO_G2=$(curl -sf "http://$GO_PPROF/debug/pprof/goroutine?debug=1" | head -1 | grep -oE "[0-9]+")
    echo "[$TS] Go → Mem: $(echo $GO_S|cut -d'|' -f1) CPU: $(echo $GO_S|cut -d'|' -f2) Goroutines: $GO_G2"
    echo "[$TS] Py → Mem: $(echo $PY_S|cut -d'|' -f1) CPU: $(echo $PY_S|cut -d'|' -f2)"
    echo "---"
    sleep 12
  done
fi
