#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

awk '
  /^pre-commit:/ { in_pre_commit = 1 }
  /^pre-push:/ { in_pre_commit = 0 }
  in_pre_commit && /run: REPO_TEMP_ENV_ISOLATE=1 scripts\/go-test-targeted\.sh \{staged_files\}/ { found = 1 }
  END { exit !found }
' "$ROOT/lefthook.yaml"
