#!/usr/bin/env bash
set -euo pipefail

module="github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2"

trim_space() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

sdk_dir="${AGORA_GO_SDK_DIR:-}"
sdk_dir="$(trim_space "$sdk_dir")"
if [ -z "$sdk_dir" ]; then
  sdk_dir="$(go list -m -f '{{.Dir}}' "$module" 2>/dev/null || true)"
fi

sdk_dir="$(trim_space "$sdk_dir")"
if [ -z "$sdk_dir" ]; then
  echo "Agora Go SDK module is not available." >&2
  echo "Run: go mod download $module" >&2
  exit 1
fi

headers=(
  "$sdk_dir/agora_sdk/include/c/api2/agora_local_user.h"
  "$sdk_dir/agora_sdk/include/c/api2/agora_media_node_factory.h"
  "$sdk_dir/agora_sdk/include/c/api2/C_IAgoraRtmLock.h"
  "$sdk_dir/agora_sdk/include/c/api2/C_IAgoraRtmHistory.h"
)
library="$sdk_dir/agora_sdk/libagora-ffmpeg.so"
missing=0
for header in "${headers[@]}"; do
  if [ ! -f "$header" ]; then
    missing=1
  fi
done
if [ ! -f "$library" ]; then
  missing=1
fi
if [ "$missing" -ne 0 ]; then
  echo "Agora native SDK payload is missing or incomplete." >&2
  for header in "${headers[@]}"; do
    echo "Expected header: $header" >&2
  done
  echo "Expected library: $library" >&2
  echo "" >&2
  echo "Install the native SDK payload in a local Agora-Golang-Server-SDK checkout:" >&2
  echo "  git clone https://github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK.git" >&2
  echo "  cd Agora-Golang-Server-SDK" >&2
  echo "  git checkout release/2.6.1" >&2
  echo "  make deps" >&2
  echo "  make install" >&2
  echo "" >&2
  echo "Then either set AGORA_GO_SDK_DIR to that checkout or add a local replace:" >&2
  echo "  replace $module => /path/to/Agora-Golang-Server-SDK" >&2
  exit 1
fi

for header in "${headers[@]}"; do
  echo "Agora native SDK header found: $header"
done
echo "Agora native SDK libraries found: $library"
echo "Set LD_LIBRARY_PATH before running agora_sdk binaries:"
echo "  export LD_LIBRARY_PATH=$sdk_dir/agora_sdk:\$LD_LIBRARY_PATH"
