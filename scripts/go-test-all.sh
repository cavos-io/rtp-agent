#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
REPO_TEMP_ENV_FORCE=1
source "$REPO_ROOT/scripts/repo-temp-env.sh"
mapfile -t packages < <(go list ./... 2>/dev/null | grep -v '/refs/ten-framework/')
go test "${packages[@]}"
