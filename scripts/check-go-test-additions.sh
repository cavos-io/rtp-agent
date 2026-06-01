#!/usr/bin/env bash
set -euo pipefail

failed=0

deleted_tests=$(git diff --cached --name-only --diff-filter=D -- '*_test.go' || true)
if [[ -n "$deleted_tests" ]]; then
  echo "Rejected: deleting Go test files is not allowed:"
  echo "$deleted_tests"
  failed=1
fi

while IFS= read -r file; do
  [[ -z "$file" ]] && continue

  stats=$(git diff --cached --numstat -- "$file" || true)
  [[ -z "$stats" ]] && continue

  additions=$(awk '{sum += $1} END {print sum+0}' <<<"$stats")
  deletions=$(awk '{sum += $2} END {print sum+0}' <<<"$stats")

  if (( additions <= deletions )); then
    echo "Rejected: $file must have more staged additions than deletions"
    echo "  additions: $additions"
    echo "  deletions:  $deletions"
    failed=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACMR -- '*_test.go')

exit "$failed"
