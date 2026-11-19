# LiveKit FFI protocol bindings

Generated Go bindings for the pinned LiveKit FFI runtime:

- Runtime: **0.12.80**
- Source: [livekit/rust-sdks](https://github.com/livekit/rust-sdks/tree/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/protocol), revision `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`
- Protocol files: `livekit-ffi/protocol/*.proto`
- Source archive SHA-256: `ccdb541f46116b36ebebce8546235a1a147659be671a919df745a61567d9eb02`
- License: [Apache-2.0](LICENSE); upstream attribution in [NOTICE](NOTICE)

Ordinary builds use these checked-in bindings without protobuf generation.
Native binaries are installed separately for the target platform and architecture.
Keep the runtime artifact and protocol bindings pinned together; a newer library
is not validated merely because it loads successfully.

To regenerate, use `protoc` and `protoc-gen-go` with the pinned source. Generator
versions are recorded in the generated file headers. For every protocol file,
provide `--go_opt=M<filename>=github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb`,
plus `--go_opt=paths=source_relative` and `--go_out` targeting this directory.
Do not edit generated files manually.

When upgrading, review protocol and C ABI changes, regenerate the bindings,
update platform artifact checksums, and rerun transport regression and live-media
checks before adopting the new pin.
