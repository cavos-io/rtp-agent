#!/usr/bin/env bash
# Install the pinned Linux FFI into an application-local directory.
set -euo pipefail
arch="${1:?usage: install-livekit-ffi-linux.sh amd64|arm64 destination [archive]}"
dest="${2:?destination required}"
case "$arch" in
  amd64) artifact=ffi-linux-x86_64.zip; checksum=81d1daa1543e25792a0dafe246d1c270611875bfc2ef544fe9be1fbafdfc77d5 ;;
  arm64) artifact=ffi-linux-arm64.zip; checksum=28cde1cb0110ed5c0ff645c8ee20321c8e6786425f4ee8b5a12c530c62c4ab4b ;;
  *) echo 'unsupported Linux architecture' >&2; exit 1 ;;
esac
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
if [ -n "${3:-}" ]; then
  cp "$3" "$work/ffi.zip"
else
  curl --silent --show-error --fail --location --retry 2 --proto '=https' --tlsv1.2 \
    "https://github.com/livekit/rust-sdks/releases/download/livekit-ffi/v0.12.80/$artifact" -o "$work/ffi.zip"
fi
actual="$(sha256sum "$work/ffi.zip")"
if [ "${actual%% *}" != "$checksum" ]; then
  echo "FFI archive checksum mismatch" >&2; exit 1
fi
unzip -q "$work/ffi.zip" -d "$work/unpacked"
lib="$(find "$work/unpacked" -type f -name liblivekit_ffi.so)"
[ -n "$lib" ] && [ -f "$lib" ]
mkdir -p "$dest"
install -m 755 "$lib" "$dest/liblivekit_ffi.so"
printf 'Installed LiveKit FFI 0.12.80 (%s)\n' "$arch"
