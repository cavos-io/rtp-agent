#!/usr/bin/env bash
set -euo pipefail
output="$(mktemp)"
trap 'rm -f "$output"' EXIT
TURN_DETECTOR_SCENARIO="$1" go test ./core/agent -run '^TestStreamingTurnReferenceScenario$' -count=1 -v > "$output" 2>&1 || { cat "$output" >&2; exit 1; }
sed -n 's/^.*TURN_TRACE //p' "$output"
