#!/usr/bin/env bash
set -euo pipefail

# Integrated Go test guard.
# Checks provided Go test files against a diff base or as full files.
#
# Enforces:
#   1. Go test files cannot be deleted (unless BASE is "all").
#   2. Modified/added/copied/renamed Go test files must have more additions than deletions.
#   3. Newly added test lines cannot introduce common test-weakening patterns.

BASE="${1:-}"
if [[ -z "$BASE" ]]; then
  echo "Usage: $0 <diff-base|all> [files...]"
  exit 1
fi
shift
FILES=("$@")

if (( ${#FILES[@]} == 0 )); then
  echo "No Go test files to check; skipping test integrity."
  exit 0
fi

failed=0

reject() {
  echo "Rejected: $*"
  failed=1
}

warn() {
  echo "Warning: $*"
}

check_deleted_test_files() {
  [[ "$BASE" == "all" ]] && return 0
  
  local deleted_tests
  deleted_tests=$(git diff "$BASE" --name-only --diff-filter=D -- "${FILES[@]}" || true)

  if [[ -n "$deleted_tests" ]]; then
    reject "deleting Go test files is not allowed:"
    echo "$deleted_tests"
  fi
}

check_test_additions_exceed_deletions() {
  [[ "$BASE" == "all" ]] && return 0
  
  local file stats additions deletions
  
  # Only check files that are present in the diff and not deleted (ACMR)
  mapfile -t targets < <(git diff "$BASE" --name-only --diff-filter=ACMR -- "${FILES[@]}" || true)

  for file in "${targets[@]}"; do
    [[ -z "$file" ]] && continue

    stats=$(git diff "$BASE" --numstat -- "$file" || true)
    [[ -z "$stats" ]] && continue

    additions=$(awk '{sum += $1} END {print sum+0}' <<<"$stats")
    deletions=$(awk '{sum += $2} END {print sum+0}' <<<"$stats")

    if (( additions <= deletions )); then
      reject "$file must have more additions than deletions"
      echo "  additions: $additions"
      echo "  deletions:  $deletions"
    fi
  done
}

check_test_weakening_patterns() {
  local added_lines

  if [[ "$BASE" == "all" ]]; then
    local existing_files=()
    for f in "${FILES[@]}"; do
      [[ -f "$f" ]] && existing_files+=("$f")
    done
    if (( ${#existing_files[@]} > 0 )); then
      added_lines=$(cat "${existing_files[@]}" | sed 's/^/+/' || true)
    else
      added_lines=""
    fi
  else
    added_lines=$(git diff "$BASE" --unified=0 -- "${FILES[@]}" | grep -E '^\+[^+]' || true)
  fi

  [[ -z "$added_lines" ]] && return 0

  if grep -E '\b(t\.Skip|t\.Skipf|SkipNow)\b' <<<"$added_lines" >/dev/null; then
    reject "test files contain or add t.Skip/t.Skipf/SkipNow"
  fi

  if grep -E '\bif[[:space:]]+testing\.Short\(\)' <<<"$added_lines" >/dev/null; then
    reject "test files contain or add testing.Short() guard"
  fi

  if grep -E '\b(assert|require)\.True\(t,[[:space:]]*true\)' <<<"$added_lines" >/dev/null; then
    reject "suspicious always-true assertion"
  fi

  if grep -E '\bif[[:space:]]+(false|true)([[:space:]\{]|$)' <<<"$added_lines" >/dev/null; then
    reject "suspicious constant condition in test"
  fi

  if grep -E '\b(assert|require)\.(Equal|NotEqual)\(t,' <<<"$added_lines" >/dev/null; then
    warn "test equality assertions found; check for accidental self-comparison."
  fi
}

check_deleted_test_files
check_test_additions_exceed_deletions
check_test_weakening_patterns

exit "$failed"
