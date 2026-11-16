# Normal Go build with opt-in native audio

Build using the existing Go/CGO toolchain and agent system dependencies:

```sh
go build -tags nativeinprocess -o agent-starter-go ./examples/starter
```

No Python packaging, probe setup, protoc, native SDK checkout, or native binary is needed to compile. Pinned generated Go FFI bindings are included under `examples/native-receive-probe/ffipb`. The experimental wrapper remains in the included nested Go module; the root go.mod resolves it locally. Do not copy only the starter folder when building.

Existing Opus/PortAudio development/runtime dependencies still apply to the starter. ONNX, Silero model and provider configuration are unchanged. CGO is required. This does not introduce a new receive playout policy or select native by default.

## macOS arm64 native library

Obtain the **0.12.80** artifact matching this schema from the upstream release. These commands install into a NEW application-local directory; no system installation is required:

```sh
mkdir livekit-ffi-0.12.80
cd livekit-ffi-0.12.80
curl -fL --retry 2 -o ffi.zip \
  https://github.com/livekit/rust-sdks/releases/download/livekit-ffi/v0.12.80/ffi-macos-arm64.zip
printf '%s\n' '980ae8b1dc687427235ce8de503068d77c6461d383beacb94592eeb2b5595386  ffi.zip' | shasum -a 256 -c -
unzip ffi.zip
printf '%s\n' '41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735  liblivekit_ffi.dylib' | shasum -a 256 -c -
cd ..
```

The pinned Rust source revision is `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`. Do not substitute another version just because its filename matches. Linux uses the matching `.so` and architecture-specific upstream artifact; this change's runtime verification is macOS arm64 only.

## Run

With your existing LiveKit, inference, ONNX and model configuration:

```sh
export RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1
export RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0
export LIVEKIT_LIB_PATH="$PWD/livekit-ffi-0.12.80/liblivekit_ffi.dylib"
./agent-starter-go start
```

The helper executable is not used in this mode. `LIVEKIT_LIB_PATH` must be an absolute path. Model paths remain relative to your working directory where applicable. Set `LIVEKIT_AGENT_NAME=agent-starter-go` for the existing load-test command.

## Helper fallback and default transport

The helper can also be built with Go without probe setup:

```sh
(cd examples/native-receive-probe && go build -o ../../native-room-helper .)
```

The existing helper includes probe publisher code and needs Opus/opusfile build dependencies. Select it after draining and restarting:

```sh
export RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=1
export RTP_AGENT_NATIVE_HELPER="$PWD/native-room-helper"
./agent-starter-go start
```

To return to default Pion, set all three experimental flags to `0`. A plain `go build -o agent-starter-go ./examples/starter` also retains the default transport and helper options, with no compiled in-process support.

## Historical evidence

Python packaging has been removed from the active workflow. Previous scripts, reports and failed attempts are preserved under `examples/native-receive-probe/results/simple-go-build-20261116/superseded-packaging/`; original experiment results and bundles remain unchanged. `INPROCESS_RESULTS.json` describes the earlier portable-bundle experiment, not this normal-build workflow. This remains experimental opt-in, not default promotion or clean-machine deployment certification.
