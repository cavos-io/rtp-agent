#!/usr/bin/env bash
set -euo pipefail

declare -A dirs=()

while IFS= read -r file; do
  [[ -z "$file" ]] && continue
  dir=$(dirname "$file")
  dirs["$dir"]=1
done < <(git diff --cached --name-only --diff-filter=ACMR -- '*.go')

if (( ${#dirs[@]} == 0 )); then
  echo "No staged Go files; skipping targeted Go tests."
  exit 0
fi

packages=()
for dir in "${!dirs[@]}"; do
  if pkg=$(go list "./$dir" 2>/dev/null); then
    packages+=("$pkg")
  fi
done

if (( ${#packages[@]} == 0 )); then
  echo "No Go packages found for staged Go files; skipping targeted Go tests."
  exit 0
fi

export GOCACHE="${GOCACHE:-/tmp/gocache}"
go test "${packages[@]}"
