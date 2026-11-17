#!/usr/bin/env bash
# Compile/run an ordinary external consumer; never initialize FFI or contact LiveKit.
set -euo pipefail
rtp_root="$(cd "$(dirname "$0")/.." && pwd)"
probe_dir="$(mktemp -d)"
trap 'rm -rf "$probe_dir"' EXIT
cat > "$probe_dir/go.mod" <<'MOD'
module native-api-consumer

go 1.26.5

require github.com/cavos-io/rtp-agent v0.0.0
MOD
GOWORK=off go mod edit -modfile="$probe_dir/go.mod" "-replace=github.com/cavos-io/rtp-agent=$rtp_root"
cat > "$probe_dir/main.go" <<'GO'
package main
import (
 "context"
 "github.com/cavos-io/rtp-agent/interface/worker"
 "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
)
func main() {
 room := nativeroom.NewInProcess(nil,nil,nativeroom.Config{Library:"unused"},"","")
 var _ worker.SessionRoomIO = room
 if err:=room.ConfigureRoomOptions(worker.RoomOptions{DisablePreConnectAudio:true});err!=nil { panic(err) }
 if err:=nativeroom.CloseInProcessRuntime(context.Background());err!=nil { panic(err) }
}
GO
GOWORK=off go build -C "$probe_dir" -mod=mod -tags nativeinprocess -o "$probe_dir/consumer" .
"$probe_dir/consumer"
echo 'Public native API external consumer passed (no probe module or native library required).'
