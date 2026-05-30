#!/usr/bin/env bash
set -euo pipefail

failed=0

# Only staged Go test files
while IFS= read -r FILE; do
  [ -z "$FILE" ] && continue

  stats=$(git diff --cached --numstat -- "$FILE" || true)
  [ -z "$stats" ] && continue

  add=$(echo "$stats" | awk '{sum += $1} END {print sum+0}')
  del=$(echo "$stats" | awk '{sum += $2} END {print sum+0}')

  if [ "$add" -le "$del" ]; then
    echo "Rejected: $FILE must have more additions than deletions"
    echo "  additions: $add"
    echo "  deletions:  $del"
    failed=1
  fi
done < <(
  git diff --cached --name-only --diff-filter=ACMR -- '*_test.go'
)

exit "$failed"