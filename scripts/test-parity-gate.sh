#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-gate-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

MANIFEST="$WORKDIR/test-cases.tsv"

cat > "$MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
dev-mode-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"env_values":["1","","true","on"]}	dev-mode-env-exact	Development mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.	Smoke test for changed-file gate selection.
TSV

bash -n "$ROOT/scripts/parity-gate.sh"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='library/utils/misc.go' \
  "$ROOT/scripts/parity-gate.sh" --changed > "$WORKDIR/changed.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/changed.out"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='docs/not-a-case.md' \
  "$ROOT/scripts/parity-gate.sh" --changed > "$WORKDIR/skipped.out" 2>&1
grep -q '^No changed files map to Layer 3 parity validation cases; skipping case validation\.$' "$WORKDIR/skipped.out"
if grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/skipped.out"; then
  echo "changed-file gate ran an unrelated case" >&2
  exit 1
fi
