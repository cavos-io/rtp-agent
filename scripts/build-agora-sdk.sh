#!/usr/bin/env bash
set -euo pipefail

module="github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2"
sdk_dir="${AGORA_GO_SDK_DIR:-}"
out="${OUT:-.tmp/rtp-agent-agora}"
modfile="${AGORA_GO_MODFILE:-.tmp/agora-sdk.mod}"
gomodcache="${GOMODCACHE:-.tmp/gomodcache}"
gocache="${GOCACHE:-.tmp/gocache}"
gotmpdir="${GOTMPDIR:-.tmp/gotmp}"

if [ -z "$sdk_dir" ]; then
  echo "AGORA_GO_SDK_DIR is required and must point to an Agora-Golang-Server-SDK checkout with native assets." >&2
  exit 1
fi

AGORA_GO_SDK_DIR="$sdk_dir" scripts/check-agora-sdk.sh >/dev/null

mkdir -p "$(dirname "$modfile")" "$(dirname "$out")" "$gomodcache" "$gocache" "$gotmpdir"
export GOMODCACHE="$(cd "$gomodcache" && pwd)"
export GOCACHE="$(cd "$gocache" && pwd)"
export GOTMPDIR="$(cd "$gotmpdir" && pwd)"
cp go.mod "$modfile"
cp go.sum "${modfile%.mod}.sum"
go mod edit -modfile="$modfile" -replace="$module=$sdk_dir"

export LD_LIBRARY_PATH="$sdk_dir/agora_sdk:${LD_LIBRARY_PATH:-}"
go build -modfile="$modfile" -tags agora_sdk -o "$out" ./cmd
echo "$out"
