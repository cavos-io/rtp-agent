#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
REPO_TEMP_ENV_FORCE=1
source "$REPO_ROOT/scripts/repo-temp-env.sh"

declare -A dirs=()

files=("$@")

for file in "${files[@]}"; do
  [[ -z "$file" ]] && continue
  dir=$(dirname "$file")
  dirs["$dir"]=1
done

if (( ${#dirs[@]} == 0 )); then
  echo "No Go files to check; skipping targeted Go tests."
  exit 0
fi

packages=()
for dir in "${!dirs[@]}"; do
  if pkg=$(go list "./$dir" 2>/dev/null); then
    packages+=("$pkg")
  fi
done

if (( ${#packages[@]} == 0 )); then
  echo "No Go packages found for targeted Go files; skipping targeted Go tests."
  exit 0
fi

go test "${packages[@]}"
