# Native transport: agent-worker-golang audit and checkout guide

Reviewed 2026-11-17. RTP Agent checkpoint: `7d531c011` (remote participant recording). Downstream: `/Users/el7cosmos/src/cavos/agent-worker-golang`, commit `f87d99c2c17fec4af3d0dd4220d2e410dbc90482`, clean working tree. Downstream currently requires RTP Agent `v0.9.3-0.20261112021132-67c11611d71b`.

## Public API update

The subsequent [public API migration](native-public-api.md) resolves the examples/internal import restriction: use `interface/worker/livekit/nativeroom`. The runtime is now in the root module, so omit the `native-receive-probe` replacement in the historical audit commands below. The audit's rejected-import evidence remains preserved. The remaining sections preserve the audit-time state. Subsequent local integration, Linux image checks and STG native calls supersede that readiness verdict; see the [current promotion checklist](native-public-api.md#default-promotion-validation-checklist).

## Readiness verdict (at audit time)

**Ready for downstream dependency/build tests using the existing Pion transport. Not ready for an end-to-end native downstream run or default promotion.**

The downstream command builds against this checkout using temporary module replacements. Focused downstream RoomOptions and automated text-input tests pass. These are compilation/unit-test results, not a dispatched call or cloud test.

Native remains directly testable through this repository's starter/local examples and native integration fixtures. The downstream cannot import `github.com/cavos-io/rtp-agent/examples/internal/nativeroom`: Go rejects it. This was reproduced with a separate-module import probe. There is no public native room constructor yet. Neither the `nativeinprocess` build tag nor the example environment flags automatically change downstream `JobContext.StartSession` calls.

## Actual RoomOptions usage

Source: downstream [app/room_options.go](../../../agent-worker-golang/app/room_options.go), with policy tests in `app/room_options_test.go`.

| Option | Downstream behavior | Native compatibility |
|---|---|---|
| `RecordRemoteParticipants` | Always true in primary application options | Implemented and locally validated for ordinary audio; see the record-only mismatch below. |
| `TranscriptionNextOutput` | Always the test-evidence publisher | Shared caption forwarding supports it, but the downstream publisher's delivery still requires connected Pion `LocalParticipant`. |
| `DisablePreConnectAudio` | True **only for telephony**; false for web and automated tests | Native rejects false. Web/automated tests currently cannot start unchanged. Explicitly disable pre-connect for a restricted native test profile, or implement it; do not change this globally. |
| `DisableTranscriptionOutput` | Depends on telephony/web `LiveTranscription`; defaults false for automated tests | Supported. Test both values while retaining test evidence output. |
| `ParticipantIdentity` | Set to tester identity for automated tests; empty otherwise | Supported, but native requires unambiguous initial caller/microphone selection. Use an explicit identity when multiple candidates exist. |
| `TextInputCallback` | Automated-test callback only | Native invokes the callback, but successful downstream turns also depend on `agent.events` evidence publication. |

Thus the earlier assumption that all downstream paths already disable pre-connect audio is incorrect. The actual conditional policy is a startup blocker.

Additional constructors: `adapter/livekit/supervisor/session.go:112` creates default Pion RoomIO for supervisor sessions. `adapter/livekit/sessionmgr/agent_session_runtime.go:186` defines a Pion RoomIO helper with `TranscriptionNextOutput`; no production call site for that helper was found in the searched checkout. No additional active primary RoomOptions assignments were found in app/interface/adapter/core production Go files.

### Remaining options versus actual requirements

The native adapter does not generally map `AudioTrackName`, configurable input/output rates/frame size, `AudioInputProcessor`, `AudioSubscriptionTimeout`, `PreConnectAudioTimeout`, `DisableAudioOutput`, `TranscriptionJSONFormat`, `DisableCloseOnDisconnect`, `RoomOptions.DeleteRoomOnClose`, custom `DeleteRoom`, or `ParticipantKinds`. `DisableAudioInput` only gates auxiliary capture; it is not complete primary-input-disable support. Native has a separate deletion property and fixed mono 24 kHz / 20 ms input, not equivalent handling of those options.

These options were not found as additional active primary overrides downstream. Do not implement all of them merely to claim parity; define the supported profile and reject unsupported non-default values. Downstream Hecttor is passed as **AgentSessionOptions.AudioInputHook** (`app/runner.go:463`), not RoomOptions.AudioInputProcessor. Native still delivers primary frames to AgentSession, but Hecttor/native interaction was not exercised by this audit.

## Room/API blockers observed in actual consumers

| Consumer / source | Actual dependency and consequence | Required migration |
|---|---|---|
| Primary startup, `app/runner.go:699–710`, `1363–1397` | Outbound SIP flow calls `job.Connect` before invitation and session startup. Custom transport rejects an already-connected job room. Current StartSession calls do not supply a Transport at all. | Add an explicit opt-in transport factory; keep native startup unconnected to Pion. Preserve SIP invitation, cancellation and recovery ordering. |
| Room callbacks, `app/runner.go:327,550`; `adapter/livekit/sessionmgr/callbacks.go:163` | Participant departure and room disconnection drive hangup reason, lifecycle cancellation and recording finalization. Native does not emit this Pion callback registry. | Supply transport-neutral lifecycle events and migrate these consumers. Native job shutdown alone does not reproduce every downstream side effect. |
| Automated evidence, `interface/worker/test_evidence.go:56–79` | Publishes reliable `agent.events` through `room.LocalParticipant.PublishDataPacket`. The prepared Pion room is not connected in native mode. | Expose a native reliable-data publisher and inject it into the evidence publisher. Preserve topic, run/message/turn correlation and error handling. Chat/captions alone are insufficient. |
| Hangup, `app/runner.go:348`; `adapter/livekit/sessionmgr/participant_repository.go:16`; `core/session/hangup/call_hangup.go:24` | Enumerates remote identities from Pion, then removes them through RoomService. An unconnected Pion room cannot supply the real participant set. | Obtain identities from native room state or a server API. Retain server-side removal and downstream termination labels/signals. |
| Transfer setup, `app/runner.go:753–817`; `adapter/livekit/sessionmgr/session_manager.go:64–80` | Customer room is supplied even before a transfer is requested; constructor snapshots `customerRoom.Name()`. Native's prepared Pion room is not a connected room metadata source. | Supply assigned job room name/identity explicitly; avoid requiring live Pion objects for initialization. |
| Hold and audio control, `adapter/livekit/sessionmgr/hold_audio_runtime.go:62–169` | BackgroundAudioPlayer starts on a Pion room; input control changes subscriptions and output control mutes local publications. | Native-backed hold publication and equivalent audio controls are needed for transfer tests. Do not silently treat empty Pion publication lists as successful control. |
| Supervisor sessions, `adapter/livekit/supervisor/session.go:42–112`; `sessionmgr/session_manager.go` | Explicit additional rooms, Pion connections and Pion RoomIO are used by transfer/supervisor workflows. | Define whether secondary rooms remain Pion in a mixed implementation or migrate them separately. They cannot be claimed native by switching the primary session. |
| Room-event logging, `app/runner.go:1404`, `1021` | Casts `PrimaryRoomIO()` to `*livekit.RoomIO`. Cast becomes nil with native. `RoomIO.On` is nil-safe, so room event logs disappear rather than necessarily panic. | Consume transport-neutral events; retain core session-close logging. |
| Report/recording segments, `app/runner.go:714–749` | Uses `PrimaryRoomIO` recorder/report methods and then enters record-only mode | Methods exist on native, but recording retention under mute is incompatible (below). |

RoomService-backed participant polling (`sessionmgr/session_manager.go:285–328`), participant removal, SIP API dialing, room deletion and egress RPCs are not intrinsically tied to Pion. Preserve these services; migrate how their room identities and lifecycle triggers are supplied. This does not certify the complete transfer flow.

### Newly identified record-only incompatibility

After a transfer merge, downstream invokes `session.SetInputAudioMuted(true)` (`app/runner.go:748`; `manager_transfer_steps.go:344`) to stop agent processing while continuing evidence recording.

Default RoomIO records the primary frame **before** `AgentSession.OnAudioFrame` (`interface/worker/livekit/room_io.go:2319–2345`). Its auxiliary recording path checks RoomIO's own audio-disabled state, not `AgentSession.InputAudioMuted`. The session mute setter only updates the session flag.

At audit time, native wrapped **both** `RecordInput` and `RecordAux` in `!Session.InputAudioMuted()` in `examples/internal/nativeroom/room.go`, suppressing record-only evidence. **Subsequently fixed:** in-process recording now precedes session mute; muted primary frames still do not reach the agent, and auxiliary frames remain recording-only. Decoded Ogg regressions reproduced both losses before the fix and passed under the race detector afterward, including an unchanged helper check. See [record-only results](../../examples/starter/NATIVE_RECORD_ONLY.md). The earlier live auxiliary fixture did not test transfer merge; complete downstream transfer validation remains outstanding.

## Use this checkout for downstream dependency tests now

Run from the downstream checkout. These commands use an alternate module file and preserve its tracked go.mod/go.sum. Existing CGO/system dependencies and private-module access are still required. No provider, cloud or native library is needed for the build/selected unit checks themselves.

```sh
cd /Users/el7cosmos/src/cavos/agent-worker-golang
RTP_CHECKOUT=/Users/el7cosmos/src/cavos/rtp-agent
AUDIT_DIR="$(mktemp -d)"
cp go.mod "$AUDIT_DIR/go.mod"
cp go.sum "$AUDIT_DIR/go.sum"
export GOWORK=off

go mod edit -modfile="$AUDIT_DIR/go.mod" \
  -replace="github.com/cavos-io/rtp-agent=$RTP_CHECKOUT" \
  -replace="native-receive-probe=$RTP_CHECKOUT/examples/native-receive-probe" \
  -replace="github.com/cavos-io/agent-worker-golang/pkg/hecttor-sdk=$PWD/pkg/hecttor-sdk"

go build -mod=mod -modfile="$AUDIT_DIR/go.mod" \
  -o "$AUDIT_DIR/agent-worker" ./cmd

go test -mod=mod -modfile="$AUDIT_DIR/go.mod" \
  ./app ./interface/worker \
  -run 'TestApplicationRoomOptionsTranscriptionPolicy|Test.*AgentTest.*Text' -count=1
```

This still builds/tests **the downstream Pion path**. It does not enable native. The explicit nested-module replacement matters: Go ignores dependency-module replace directives. An rtp-agent replacement alone is not a complete recipe for its experimental native dependency. The absolute Hecttor replacement preserves the existing local SDK dependency while using an alternate module file.

The audit used equivalent commands with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local` and an existing writable build cache; it did not install dependencies or start downstream services. An initial successful build emitted a sandbox module-cache metadata warning; that output is retained alongside the clean retry. Missing cached dependencies in another environment require the repository's normal dependency setup.

### TextStreamInfo migration

`livekit.TextInputEvent.Info` is now `*ffipb.TextStreamInfo` from `github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb`.

- Downstream currently reads only `event.Info.Attributes`, which still compiles; the focused callback tests pass. Prefer `event.Info.GetAttributes()` for nil-safe handling of synthetic events.
- Other consumers using `Info.Id`, `Info.Size`, or SDK TextStreamInfo literals must migrate to `GetStreamId()`, `GetTotalLength()` and protobuf values. `TotalLength == nil` retains unknown-length semantics.
- Native keeps FFI metadata. Pion conversion supplies protocol defaults for operation/encryption fields unavailable from its SDK metadata. See [native chat migration](../../examples/starter/NATIVE_CHAT.md).

No SDK patch, unsafe reflection, or protobuf generation step is needed.

## Native feature testing available now

Inside **this RTP Agent checkout**, build the starter normally with the native tag:

```sh
go build -tags nativeinprocess -o /tmp/agent-starter-native ./examples/starter
```

Use the pinned macOS arm64 FFI 0.12.80 and existing model/provider setup described in [the build/run guide](../../examples/starter/packaging/INPROCESS.md). For your ignored local example, build the whole package (`./examples/local`), not only `main.go`, so its tagged startup files are included.

The example flags select native only in these examples. `RecordRemoteParticipants`, the application TextInputCallback, tester identity and transcription forwarding must be passed through StartSession's RoomOptions; the starter's default options do not automatically reproduce downstream policy.

This can validate native audio, captions, state, chat and recording in isolation. It cannot validate downstream `agent.events`, hangup outcomes, SIP invitation or transfer behavior until the integration blockers above are addressed. Keep every run in a new results directory.

## Smallest migration sequence

1. Export a supported native transport/runtime API under `interface/worker`, removing the examples/internal import restriction and eventually the probe-module runtime dependency. Keep default and helper selectors unchanged.
2. Record-only retention is now fixed and regression-tested; include recording segmentation and transfer merge in downstream validation after API migration. Keep the existing recording gates.
3. Add an opt-in downstream factory and worker-owned shutdown after session drain. For the first restricted web/automated profile, explicitly disable pre-connect audio, keep a selected microphone publisher, and wire native reliable `agent.events` plus room lifecycle and hangup APIs. The current automated tester publishes a microphone (`adapter/test/dispatch/livekit/media.go:129–140`), so its startup is not inherently text-only; arbitrary chat-only clients remain unsupported.
4. Migrate unconditional room-name/transfer initialization and either implement hold/transfer controls or explicitly reject those workflows in the restricted profile. Do not connect a second Pion agent to make missing APIs appear to work.
5. Validate real downstream cloud sessions, then distinct-marker concurrent recording/chat/reconnect and worker drain. Expand to SIP invitation, transfer/supervisor and recording segmentation only after their prerequisites pass.

Retain Pion as the broader compatibility fallback. The helper remains an isolation fallback, not a replacement for missing room features. Rollback is drain/restart with the existing default path; no live transport switch is proposed.

## Evidence and scope

Local source inspection covered `app`, `interface`, `adapter` and `core` in agent-worker-golang plus RTP Agent's worker/native boundaries. Indexed repository/semantic search was attempted but blocked by sandbox keychain access; findings were verified against full local files, not index snippets. No other downstream repository was audited. No credentials/configuration files were read, no downstream source was changed, and no live downstream/cloud session was run.

[Audit manifest](../../examples/starter/NATIVE_DOWNSTREAM_AUDIT_RESULTS.json) records revisions, build/test outcomes, the expected rejected internal import, preservation checks and artifact hashes. Existing successful and failed native results remain unchanged.
