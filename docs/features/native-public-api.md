# Public in-process native transport

The experimental transport is now importable by ordinary downstream modules:

```go
import "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
```

Build with `-tags nativeinprocess` and CGO on the validated macOS arm64 host. The public constructor and shutdown API retain their existing signatures. Moving ownership does not make native the default or make all downstream workflows compatible.

## Session startup

Use this inside your opt-in downstream job startup branch, with the existing session and API credentials. Keep the current StartSession path for the default branch.

```go
transport := nativeroom.NewInProcess(job, session, nativeroom.Config{
    Library:     libraryPath, // absolute path to pinned liblivekit_ffi.dylib
    Participant: callerIdentity,
}, apiKey, apiSecret)

opts := applicationRoomOptions // preserve captions, callback and recording options
opts.DisablePreConnectAudio = true // explicit restriction of the native test profile
err := job.StartSession(ctx, session, worker.StartSessionOptions{
    SessionContext: ctx,
    Transport:      transport,
    RoomOptions:   opts,
})
```

This is integration guidance, not an existing downstream selector. `RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS` is interpreted by the examples, not automatically by JobContext. `applicationRoomOptions`, caller identity and library selection are supplied by your composition code.

Do not call `job.Connect` first. Custom transport requires an unconnected Pion job room and joins using the assigned primary token. A selected microphone is required; multiple initial microphone candidates require explicit participant selection. `Config.URL`, `Token`, and `TrackSID` overrides exist for fixtures; ordinary workers should use assigned job connection information and discovery.

The transport implements `worker.SessionRoomIO`, including recording/report hooks. Use `RoomOptions` for the supported settings, not direct Pion room access. `transport.DeleteRoomOnClose` is a separate opt-in property; it does not implement the custom RoomOptions deletion callback contract.

## Worker shutdown

After draining and closing every native session, call:

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := nativeroom.CloseInProcessRuntime(shutdownCtx); err != nil {
    // Report shutdown failure through the worker's logger/error result.
}
```

Arrange defer order so server/session cleanup happens before runtime disposal. Do not call this per session. Native is process-global, cannot coexist with another FFI owner, and cannot be reinitialized after disposal. A native crash still affects the worker process. Context bounds waiting; it cannot forcibly terminate a blocked native call.

## Use a local checkout downstream

From `agent-worker-golang`, use temporary module files to preserve its dependency files:

```sh
RTP_CHECKOUT=/Users/el7cosmos/src/cavos/rtp-agent
TEST_MOD_DIR="$(mktemp -d)"
cp go.mod "$TEST_MOD_DIR/go.mod"
cp go.sum "$TEST_MOD_DIR/go.sum"
export GOWORK=off

go mod edit -modfile="$TEST_MOD_DIR/go.mod" \
  -replace="github.com/cavos-io/rtp-agent=$RTP_CHECKOUT" \
  -replace="github.com/cavos-io/agent-worker-golang/pkg/hecttor-sdk=$PWD/pkg/hecttor-sdk"

go build -mod=mod -modfile="$TEST_MOD_DIR/go.mod" -tags nativeinprocess \
  -o "$TEST_MOD_DIR/agent-worker" ./cmd
```

**No `native-receive-probe` requirement/replacement is needed now.** The runtime moved into the root module. Downstream must still add its explicit native transport branch; the build tag alone does not change runtime selection.

Keep the pinned FFI 0.12.80 library and platform checksum instructions from the [build guide](../../examples/starter/packaging/INPROCESS.md). Existing Opus/PortAudio, model and provider dependencies still apply. No Python packaging or probe setup is needed. Linux runtime support remains unvalidated by this migration.

## Import migration and fallback

- `examples/internal/nativeroom` moved to `interface/worker/livekit/nativeroom`.
- `native-receive-probe/nativeinprocess` moved to `interface/worker/livekit/nativeinprocess`. Applications should use the room facade; direct runtime consumers must respect its single-owner lifecycle.
- Shared `ffipb` stays at `interface/worker/livekit/ffipb`.
- `NewInProcess` and `CloseInProcessRuntime` remain behind the `nativeinprocess` tag. `New` keeps the helper-process implementation available without that tag.
- Starter and local-example imports were updated. Historical artifacts retain their original paths and commands; they describe the versions tested then.
- Default Pion selection and helper behavior are unchanged. Rollback remains drain/restart using the default or helper branch, not a live switch.

## Readiness and evidence

The external-module import restriction is resolved. A separate-module program builds with only the root-checkout replacement, constructs a SessionRoomIO-compatible native transport and calls shutdown without loading FFI. A public-package test validates configuration without opening native resources. Existing native, helper and starter regressions are moved with the implementation and run under the race detector.

This is an ownership/API migration, not new cloud or native-library runtime validation. The downstream blockers in the [audit](native-downstream-compatibility.md) still apply: reliable `agent.events`, room lifecycle/hangup APIs, pre-connect policy, initial SIP connection ordering, hold/transfer/supervisor integration, and worker shutdown wiring. Record-only mute retention was fixed previously. Do not promote native to the default based on successful imports alone.

Run `scripts/test-native-public-api.sh` to repeat the separate-module import/configuration/shutdown check without loading a native library.

Evidence, including the initial worker-boundary placement failure, is preserved in `examples/native-receive-probe/results/public-native-api-20261117/` and [the manifest](../../examples/starter/NATIVE_PUBLIC_API_RESULTS.json).

## Supported options and embedded shutdown

Native now rejects explicit unsupported `RoomOptions` before acquiring media.
The supported fields are `DisablePreConnectAudio` (must be true),
`ParticipantIdentity`, `RecordRemoteParticipants`, `DisableTextInput`,
`TextInputCallback`, `DisableTranscriptionOutput`, and `TranscriptionNextOutput`.
All other fields must remain zero/nil/empty. This includes audio input/output
disable flags: session mute controls are separate and retain recording.
Native audio uses its existing fixed profile; specifying a custom rate, frame
size, processor or track name is rejected, even if the value resembles a default.
The helper and default Pion option handling are unchanged.

Embedded applications that own FFI should call `cli.RunAppWithOptionsError`.
It returns command errors after server closure, allowing application defers to
run. Dispose the native runtime only after successful server drain/closure, using
a fresh bounded context. `cli.RunAppWithOptions` preserves standalone process-exit
behavior for existing consumers. SIGKILL and a blocked native call cannot be
turned into graceful disposal by this API.

The downstream worker integration now supplies participant lifecycle, reliable
data and hold controls described in its `docs/native-transport.md`. The older
audit above is historical. Linux runtime/image checks are recorded in
[validation evidence](../results/native-linux-validation-20261118/README.md);
remaining live downstream acceptance still gates promotion. Pion remains the default.


## Hold diagnostics

With native enabled, `native.hold.published` confirms publication acknowledgement.
`native.hold.pcm_delivered` reports the first successful capture, then every 500
frames (about ten seconds for 20 ms frames), with frame count, PCM duration and
that frame's absolute peak amplitude. These counters measure native SDK
acknowledgements, not remote playback. `native.hold.stopped` reports final counts
and whether clearing succeeded. Publication, capture and clearing errors are
reported separately.

STG browser call `1117981` on 2026-11-18 published hold at 07:58:00 Asia/Jakarta,
delivered 1,470 frames / 29.4 seconds of PCM, then cleared successfully at
07:58:30 and played the transfer-unavailable message. The tester confirmed
hearing hold audio and intentionally did not answer the transferred call.
This covers audible browser hold and the unanswered-transfer path; it does not
establish an answered handoff for this run.
