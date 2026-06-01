#!/usr/bin/env bash
set -euo pipefail

# Integrated staged Go test guard.
# Checks only staged changes to *_test.go files.
#
# Enforces:
#   1. Go test files cannot be deleted.
#   2. Modified/added/copied/renamed Go test files must have more additions than deletions.
#   3. Newly added test lines cannot introduce common test-weakening patterns.

failed=0

reject() {
  echo "Rejected: $*"
  failed=1
}

warn() {
  echo "Warning: $*"
}

check_deleted_test_files() {
  local deleted_tests
  deleted_tests=$(git diff --cached --name-only --diff-filter=D -- '*_test.go' || true)

  if [[ -n "$deleted_tests" ]]; then
    reject "deleting Go test files is not allowed:"
    echo "$deleted_tests"
  fi
}

check_test_additions_exceed_deletions() {
  local file stats additions deletions

  while IFS= read -r file; do
    [[ -z "$file" ]] && continue

    stats=$(git diff --cached --numstat -- "$file" || true)
    [[ -z "$stats" ]] && continue

    additions=$(awk '{sum += $1} END {print sum+0}' <<<"$stats")
    deletions=$(awk '{sum += $2} END {print sum+0}' <<<"$stats")

    if (( additions <= deletions )); then
      reject "$file must have more staged additions than deletions"
      echo "  additions: $additions"
      echo "  deletions:  $deletions"
    fi
  done < <(git diff --cached --name-only --diff-filter=ACMR -- '*_test.go')
}

check_test_weakening_patterns() {
  local added_lines

  added_lines=$(git diff --cached --unified=0 -- '*_test.go' \
    | grep -E '^\+[^+]' || true)

  [[ -z "$added_lines" ]] && return 0

  if grep -E '\b(t\.Skip|t\.Skipf|SkipNow)\b' <<<"$added_lines" >/dev/null; then
    reject "staged test files add t.Skip/t.Skipf/SkipNow"
  fi

  if grep -E '\bif[[:space:]]+testing\.Short\(\)' <<<"$added_lines" >/dev/null; then
    reject "staged test files add testing.Short() guard"
  fi

  if grep -E '\b(assert|require)\.True\(t,[[:space:]]*true\)' <<<"$added_lines" >/dev/null; then
    reject "suspicious always-true assertion"
  fi

  if grep -E '\bif[[:space:]]+(false|true)([[:space:]\{]|$)' <<<"$added_lines" >/dev/null; then
    reject "suspicious constant condition in test"
  fi

  if grep -E '\b(assert|require)\.(Equal|NotEqual)\(t,' <<<"$added_lines" >/dev/null; then
    warn "staged tests add equality assertions; check for accidental self-comparison."
  fi
}

check_deleted_test_files
check_test_additions_exceed_deletions
check_test_weakening_patterns

exit "$failed"
