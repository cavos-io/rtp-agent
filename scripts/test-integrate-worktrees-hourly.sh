#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/integrate-worktrees-hourly.sh"

bash -n "$SCRIPT"

if grep -Fq 'GOCACHE=${GOCACHE:-$REPO_ROOT/.tmp}' "$SCRIPT"; then
  echo "integrate-worktrees-hourly.sh must not reuse main repo .tmp for worktree tests" >&2
  exit 1
fi

grep -Fq 'local temp_root="$path/.tmp"' "$SCRIPT"
grep -Fq 'GOCACHE=$temp_root' "$SCRIPT"
grep -Fq 'TMPDIR=$temp_root/gotmp' "$SCRIPT"
