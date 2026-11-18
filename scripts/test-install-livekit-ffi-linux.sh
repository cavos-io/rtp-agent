#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
printf 'invalid archive' > "$work/bad.zip"
if bash "$root/scripts/install-livekit-ffi-linux.sh" amd64 "$work/output" "$work/bad.zip"; then
  echo 'accepted corrupt archive' >&2; exit 1
fi
[ ! -e "$work/output" ]
if bash "$root/scripts/install-livekit-ffi-linux.sh" unsupported "$work/output" "$work/bad.zip"; then
  echo 'accepted unsupported architecture' >&2; exit 1
fi
[ ! -e "$work/output" ]
echo 'Installer rejects corrupt archives and unsupported architectures before installation.'
