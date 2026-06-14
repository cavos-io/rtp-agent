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
if [ -z "$(trim_space "$sdk_dir")" ]; then
  sdk_dir="$(go list -m -f '{{.Dir}}' "$module" 2>/dev/null || true)"
fi

if [ -z "$(trim_space "$sdk_dir")" ]; then
  echo "Agora Go SDK module is not available." >&2
  echo "Run: go mod download $module" >&2
  exit 1
fi

header="$sdk_dir/agora_sdk/include/c/api2/agora_local_user.h"
library="$sdk_dir/agora_sdk/libagora-ffmpeg.so"
if [ ! -f "$header" ] || [ ! -f "$library" ]; then
  echo "Agora native SDK payload is missing or incomplete." >&2
  echo "Expected header: $header" >&2
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

echo "Agora native SDK headers found: $header"
echo "Agora native SDK libraries found: $library"
echo "Set LD_LIBRARY_PATH before running agora_sdk binaries:"
echo "  export LD_LIBRARY_PATH=$sdk_dir/agora_sdk:\$LD_LIBRARY_PATH"
