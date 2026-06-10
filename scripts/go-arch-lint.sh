#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"
REPO_TEMP_ENV_FORCE=1
source "$REPO_ROOT/scripts/repo-temp-env.sh"

exec go-arch-lint "$@"
