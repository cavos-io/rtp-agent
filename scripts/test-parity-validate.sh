#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-validate-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

VALID_MANIFEST="$WORKDIR/test-cases.tsv"
BAD_MANIFEST="$WORKDIR/bad-test-cases.tsv"
INCOMPLETE_CROSS_MANIFEST="$WORKDIR/incomplete-cross-test-cases.tsv"

cat > "$VALID_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
dev-mode-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./library/utils/cmd/parity-utils	{"env_values":["1","","true","on"]}	dev-mode-env-exact	Development mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.	Smoke test for real cross-runtime runner dispatch.
TSV

cat > "$BAD_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
bad	go-test	source	target	./pkg	TestName				contract	behavior	notes	unexpected-tab-field
TSV

cat > "$INCOMPLETE_CROSS_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
missing-runner	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc_test.go				go-runner	input.json	json-contract	dev mode env contract	must fail before placeholder dispatch when a runner is absent
TSV

bash -n "$ROOT/scripts/parity-validate.sh"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'dev-mode-cross'

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case dev-mode-cross > "$WORKDIR/cross.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" > "$WORKDIR/all-cross.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/all-cross.out"

if PARITY_TEST_CASES_FILE="$INCOMPLETE_CROSS_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case missing-runner > "$WORKDIR/incomplete-cross.out" 2>&1; then
  echo "cross-runtime case with a missing runner unexpectedly passed" >&2
  exit 1
fi
grep -q 'cross-runtime rows must set source_ref, target_ref, python_runner, go_runner, input_json, contract, and behavior' "$WORKDIR/incomplete-cross.out"

if PARITY_TEST_CASES_FILE="$BAD_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list > "$WORKDIR/bad.out" 2>&1; then
  echo "manifest with an extra tab field unexpectedly passed" >&2
  exit 1
fi
grep -q 'Tabs are not allowed inside manifest fields' "$WORKDIR/bad.out"
