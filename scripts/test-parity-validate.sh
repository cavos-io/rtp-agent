#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-validate-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

VALID_MANIFEST="$WORKDIR/test-cases.tsv"
BAD_MANIFEST="$WORKDIR/bad-test-cases.tsv"

cat > "$VALID_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
tiny-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc_test.go			python-runner	go-runner	input.json	json-contract	dev mode env contract	placeholder must fail until runners execute both sides
TSV

cat > "$BAD_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
bad	go-test	source	target	./pkg	TestName				contract	behavior	notes	unexpected-tab-field
TSV

bash -n "$ROOT/scripts/parity-validate.sh"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'tiny-cross'

if PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case tiny-cross > "$WORKDIR/cross.out" 2>&1; then
  echo "cross-runtime placeholder case unexpectedly passed" >&2
  exit 1
fi
grep -q 'cross-runtime validation is not implemented yet' "$WORKDIR/cross.out"
grep -q 'do not prove behavior' "$WORKDIR/cross.out"

if PARITY_TEST_CASES_FILE="$BAD_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list > "$WORKDIR/bad.out" 2>&1; then
  echo "manifest with an extra tab field unexpectedly passed" >&2
  exit 1
fi
grep -q 'Tabs are not allowed inside manifest fields' "$WORKDIR/bad.out"
