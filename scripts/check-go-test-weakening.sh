#!/usr/bin/env bash
set -euo pipefail

failed=0
added_lines=$(git diff --cached --unified=0 -- '*_test.go' \
  | grep -E '^\+[^+]' || true)

if [[ -z "$added_lines" ]]; then
  exit 0
fi

if grep -E '\b(t\.Skip|t\.Skipf|SkipNow)\b' <<<"$added_lines" >/dev/null; then
  echo "Rejected: staged test files add t.Skip/t.Skipf/SkipNow"
  failed=1
fi

if grep -E '\bif[[:space:]]+testing\.Short\(\)' <<<"$added_lines" >/dev/null; then
  echo "Rejected: staged test files add testing.Short() guard"
  failed=1
fi

if grep -E '\b(assert|require)\.True\(t,[[:space:]]*true\)' <<<"$added_lines" >/dev/null; then
  echo "Rejected: suspicious always-true assertion"
  failed=1
fi

if grep -E '\bif[[:space:]]+(false|true)([[:space:]\{]|$)' <<<"$added_lines" >/dev/null; then
  echo "Rejected: suspicious constant condition in test"
  failed=1
fi

if grep -E '\b(assert|require)\.(Equal|NotEqual)\(t,' <<<"$added_lines" >/dev/null; then
  echo "Warning: staged tests add equality assertions; check for accidental self-comparison."
fi

exit "$failed"
