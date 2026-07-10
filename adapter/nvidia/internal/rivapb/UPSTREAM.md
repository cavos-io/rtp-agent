# NVIDIA Riva protobuf bindings

Generated from `https://github.com/nvidia-riva/common.git` at commit
`71df98266725320a6b6b3a9f32a6da832dc93691`.

Inputs:

- `riva/proto/riva_audio.proto`
- `riva/proto/riva_common.proto`
- `riva/proto/riva_asr.proto`

Tool versions:

- `libprotoc 29.4`
- `protoc-gen-go v1.36.11`
- `protoc-gen-go-grpc 1.6.0`

Regenerate from the repository root:

```sh
protoc -I /tmp/nvidia-riva-common-71df982 \
  --go_out=. \
  --go_opt=module=github.com/cavos-io/rtp-agent \
  --go_opt=Mriva/proto/riva_audio.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go_opt=Mriva/proto/riva_common.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go_opt=Mriva/proto/riva_asr.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/cavos-io/rtp-agent \
  --go-grpc_opt=Mriva/proto/riva_audio.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_opt=Mriva/proto/riva_common.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_opt=Mriva/proto/riva_asr.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_audio.proto \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_common.proto \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_asr.proto
```
