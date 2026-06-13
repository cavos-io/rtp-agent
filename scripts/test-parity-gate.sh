#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-gate-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

MANIFEST="$WORKDIR/test-cases.tsv"
SCENARIO="$WORKDIR/dev-mode-json-scenario.json"

cat > "$SCENARIO" <<'JSON'
{
  "name": "dev-mode-json-scenario",
  "case_type": "cross-runtime",
  "input": {"env_values": ["1", "", "true", "on"]},
  "python_entrypoint": "scripts.parity_scenario_entries:dev_mode_env_exact",
  "go_handler": "dev_mode_env_exact",
  "compare_mode": "json_equal",
  "ignored_fields": ["timestamp", "duration", "trace_id"]
}
JSON

cat > "$MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
dev-mode-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"env_values":["1","","true","on"]}	dev-mode-env-exact	Development mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.	Smoke test for changed-file gate selection.
TSV
printf 'dev-mode-json-scenario\tjson-scenario\trefs/agents/livekit-agents/livekit/agents/utils/misc.py\tscripts/parity-runners/json-scenario\t\t\tpython3 scripts/parity-runners/json-scenario-python.py\tgo run ./scripts/parity-runners/json-scenario\t%s\tdev-mode-env-exact\tDevelopment mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.\tSmoke test for changed-file JSON scenario selection.\n' "$SCENARIO" >> "$MANIFEST"

bash -n "$ROOT/scripts/parity-gate.sh"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='library/utils/misc.go' \
  "$ROOT/scripts/parity-gate.sh" --changed > "$WORKDIR/changed.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/changed.out"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES="$SCENARIO" \
  "$ROOT/scripts/parity-gate.sh" --changed > "$WORKDIR/json-scenario-changed.out" 2>&1
grep -q '^\[dev-mode-json-scenario\] ok$' "$WORKDIR/json-scenario-changed.out"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='docs/not-a-case.md' \
  "$ROOT/scripts/parity-gate.sh" --changed > "$WORKDIR/skipped.out" 2>&1
grep -q '^No changed files map to Layer 3 parity validation cases; skipping case validation\.$' "$WORKDIR/skipped.out"
if grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/skipped.out"; then
  echo "changed-file gate ran an unrelated case" >&2
  exit 1
fi

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='docs/not-a-case.md' \
  "$ROOT/scripts/parity-gate.sh" --changed --quick > "$WORKDIR/quick.out" 2>&1
grep -q '^Quick gate: skipping scripts/check-deadcode.sh\. Run scripts/parity-gate.sh before completion\.$' "$WORKDIR/quick.out"

PARITY_TEST_CASES_FILE="$MANIFEST" \
PARITY_GATE_CHANGED_FILES='library/utils/misc.go' \
  "$ROOT/scripts/parity-gate.sh" --local > "$WORKDIR/local.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/local.out"
grep -q '^Quick gate: skipping scripts/check-deadcode.sh\. Run scripts/parity-gate.sh before completion\.$' "$WORKDIR/local.out"
