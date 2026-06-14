#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/agora-sdk-scripts-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

bash -n "$ROOT/scripts/check-agora-sdk.sh" "$ROOT/scripts/build-agora-sdk.sh"

mkdir -p "$WORKDIR/sdk/agora_sdk/include/c/api2"
: >"$WORKDIR/sdk/agora_sdk/include/c/api2/agora_local_user.h"
: >"$WORKDIR/sdk/agora_sdk/libagora-ffmpeg.so"

if AGORA_GO_SDK_DIR="   " OUT="$WORKDIR/rtp-agent-agora" "$ROOT/scripts/build-agora-sdk.sh" >"$WORKDIR/out-blank-sdk.txt" 2>"$WORKDIR/err-blank-sdk.txt"; then
  echo "build-agora-sdk.sh unexpectedly passed with blank SDK dir" >&2
  exit 1
fi

grep -q '^AGORA_GO_SDK_DIR is required and must point to an Agora-Golang-Server-SDK checkout with native assets\.$' "$WORKDIR/err-blank-sdk.txt"

if ! AGORA_GO_SDK_DIR="  $WORKDIR/sdk  " "$ROOT/scripts/check-agora-sdk.sh" >"$WORKDIR/out-padded-check.txt" 2>"$WORKDIR/err-padded-check.txt"; then
  echo "check-agora-sdk.sh did not accept a padded SDK dir" >&2
  cat "$WORKDIR/err-padded-check.txt" >&2
  exit 1
fi

grep -q "Agora native SDK headers found: $WORKDIR/sdk/agora_sdk/include/c/api2/agora_local_user.h" "$WORKDIR/out-padded-check.txt"
