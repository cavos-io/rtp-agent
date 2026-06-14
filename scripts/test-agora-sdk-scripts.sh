#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/agora-sdk-scripts-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

bash -n "$ROOT/scripts/check-agora-sdk.sh" "$ROOT/scripts/build-agora-sdk.sh"

if AGORA_GO_SDK_DIR="   " OUT="$WORKDIR/rtp-agent-agora" "$ROOT/scripts/build-agora-sdk.sh" >"$WORKDIR/out-blank-sdk.txt" 2>"$WORKDIR/err-blank-sdk.txt"; then
  echo "build-agora-sdk.sh unexpectedly passed with blank SDK dir" >&2
  exit 1
fi

grep -q '^AGORA_GO_SDK_DIR is required and must point to an Agora-Golang-Server-SDK checkout with native assets\.$' "$WORKDIR/err-blank-sdk.txt"
