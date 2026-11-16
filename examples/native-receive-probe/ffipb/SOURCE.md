# Pinned LiveKit FFI protocol bindings

Generated from livekit/rust-sdks revision `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, `livekit-ffi/protocol/*.proto`, paired with LiveKit FFI **0.12.80**. Source archive SHA256: `ccdb541f46116b36ebebce8546235a1a147659be671a919df745a61567d9eb02`.

These generated Go files are included as source so ordinary builds require neither protobuf generation nor probe setup. Apache-2.0 license and upstream notice included. Do not edit generated files manually.

Maintainer-only regeneration requires protoc and protoc-gen-go, with the pinned upstream source extracted. For each protocol file, pass `--go_opt=M<filename>=native-receive-probe/ffipb`, along with `--go_opt=paths=source_relative` and `--go_out` pointing here. The optional probe `generate.py` does this mapping; it is not part of the application build. Generator versions are recorded in every generated file.

The historical ignored `internal/ffipb` cache is no longer imported. No native binaries are included in Go source; select a matching platform/architecture artifact separately at runtime.
