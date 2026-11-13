#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
for tool in go protoc protoc-gen-go uv ffmpeg curl tar unzip; do command -v "$tool" >/dev/null || { echo "Missing tool: $tool (install separately)" >&2; exit 1; }; done
[[ "$(uname -s)-$(uname -m)" == Darwin-arm64 ]] || { echo 'Bootstrap currently verified only on macOS ARM64.' >&2; exit 1; }
mkdir -p .cache
revision=5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb
[[ -f .cache/source.tar.gz ]] || curl -fL --retry 2 "https://github.com/livekit/rust-sdks/archive/$revision.tar.gz" -o .cache/source.tar.gz
[[ -f .cache/ffi.zip ]] || curl -fL --retry 2 https://github.com/livekit/rust-sdks/releases/download/livekit-ffi/v0.12.80/ffi-macos-arm64.zip -o .cache/ffi.zip
printf '%s\n' 'ccdb541f46116b36ebebce8546235a1a147659be671a919df745a61567d9eb02  .cache/source.tar.gz' '980ae8b1dc687427235ce8de503068d77c6461d383beacb94592eeb2b5595386  .cache/ffi.zip' | shasum -a 256 -c -
tar -xzf .cache/source.tar.gz -C .cache
unzip -oq .cache/ffi.zip -d .cache/native
[[ -d .venv ]] || uv venv --python 3.14.4 .venv
uv pip install --python .venv/bin/python -r requirements.txt
.venv/bin/python generate.py
export GOCACHE="$PWD/.cache/go-build"
go mod download
go build -o probe .
# Upstream Go Opus test fixture, looped because its original duration is 10.8s.
fixture="$(go env GOMODCACHE)/github.com/hraban/opus@v0.0.0-20251117090126-c76ea7e21bf3/testdata/speech_8.wav"
ffmpeg -v error -stream_loop -1 -i "$fixture" -t 20 -ar 48000 -ac 1 -f s16le -y .cache/speech.pcm
