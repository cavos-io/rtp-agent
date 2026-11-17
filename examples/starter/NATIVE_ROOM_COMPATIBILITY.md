# In-process native room compatibility review

Reviewed 2026-11-17 against `fdc689de9`. Source review only; no new live run or default change. Native FFI 0.12.80, rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`; runtime evidence is macOS arm64. The comparison is with this repository's default Pion room adapter, not a claim of complete Python Agents parity.

The latest downstream audit is [agent-worker-golang compatibility and migration](../../docs/features/native-downstream-compatibility.md), against RTP Agent `7d531c011` and downstream `f87d99c2c17f`. Captions, state, chat, DTMF and auxiliary recording now have implementations; the original source review below is historical context. The downstream build and focused tests pass with a local checkout replacement, but **native downstream integration is not ready**: the transport is still examples/internal, application room APIs depend on Pion, web/automated tests leave pre-connect audio enabled, and transfer/lifecycle APIs still require migration. The subsequently fixed record-only mute issue has regression coverage in [NATIVE_RECORD_ONLY.md](NATIVE_RECORD_ONLY.md).

**Recommendation: keep native opt-in.** The current local voice application has evidence for its audio loop, but the native adapter is not a drop-in replacement for the room features exposed by the default adapter. Decide the supported client contract before implementing optional features.

## What the current app actually uses

The inspected, Git-ignored `examples/local/main.go` manually constructs a Silero VAD, Deepgram STT, Azure LLM and LiveKit TTS agent, a static weather tool, an initial greeting, and session reporting. It enables TTS-aligned transcripts in the agent session. Its in-process startup sets `DeleteRoomOnClose=true`. Commented-out providers/detectors are not active requirements. Reading `app.DefaultConfigFromEnv()` does not activate every feature in `app.App` when the example constructs its own session.

This requires bidirectional audio, interruption/playback accounting, caller lifecycle, recording/reporting, and room deletion. The weather tool does not access the room. No explicit text-chat, RPC, SIP/DTMF, avatar, or video consumer was found in this local entrypoint. External client requirements are not established by that observation: live captions and speaking/listening indicators may be consumed implicitly by a UI. Session-report transcripts and `UseTTSAlignedTranscript` do **not** prove room captions are published.

The tracked starter uses the same transport selector with different provider/session configuration. Unlike local, its native startup does not enable room deletion. This review does not infer other deployments' requirements from either example.

## Compatibility matrix

“Missing” below means missing from our adapter/wrapper, not necessarily from LiveKit's native SDK.

| Feature | Current native behavior / evidence | Requirement and remaining work |
|---|---|---|
| One agent room connection; input/output | Worker-assigned primary token; native owns both directions. Cloud and local tests demonstrate two participants. | Required; covered for tested audio scenarios. |
| Continuous input to Go agent | Mono 24 kHz / 20 ms frames; bounded session delivery and stall failure. Existing cloud and concurrent-soak continuity evidence. | Required; covered within measured limits, not proof of DTX/loss or content identity in every cloud run. |
| Output, pause/resume, interruption, false recovery | Shared native playback adapter implements capture/clear, accounting and session controllers. Controlled recovery tests plus cloud observation. | Required; preserve existing gates. Playback accounting is not an acknowledgement of sound rendered by a remote device. |
| Greeting / remote output subscription | In-process capture now waits for native output subscription, with the default 10-second timeout fallback; delayed subscription validated locally. | Event does not identify the subscriber or acknowledge speaker rendering. See [audio lifecycle evidence](NATIVE_AUDIO_LIFECYCLE.md). |
| Normal departure / close | Selected caller departure or unsubscribe outside reconnect ends job; expected closure classified normally. Latest cloud shutdown validation passed. | Required; covered for tested departure ordering. |
| Native reconnect | Selected participant can rebind; in-process microphone mode also supports replacement SIDs. Native owns retries; wrapper has 30 s lifecycle bound. | Accepted temporary multi-second gap remains. Existing failed uninterrupted/gap gates must remain failed, not be relabeled passing. |
| Mute/unmute, republish, device change | In-process muted startup and mute/unmute retain the sink; changed microphone SID replaces input without ending the caller session. Local race-enabled tests passed. | Replacement may introduce a media gap (391 ms measured). Existing 30-second rebind bound applies; browser hardware/cloud validation remains. |
| Caller/track selection | In-process discovery accepts muted/unmuted microphone audio and excludes screen-share/video/unknown sources. Selected caller is retained; active screen marker exclusion passed locally. | Requires API credentials; multiple initial microphone candidates remain ambiguous. Helper selection is unchanged. |
| Live captions | Implemented for in-process mode using the default caption engine; transcription packets and `lk.transcription` streams verified in a local two-participant room. | Required by the user. User-reported frontend caption validation exists; combined downstream workflow validation remains. Helper-primary caption behavior is unchanged. See NATIVE_CAPTIONS.md. |
| Client state | Implemented for in-process mode: `lk.agent.state` attribute and reliable `lk-agent-state` messages, with current-state refresh on reconnect. | Local wire delivery and lifecycle regressions passed; frontend/cloud permission validation remains. See [NATIVE_CLIENT_STATE.md](NATIVE_CLIENT_STATE.md). |
| Text input / pre-connect audio | In-process `lk.chat` supports TextInputCallback, default turn handling and DisableTextInput. Shared callback metadata uses protobuf TextStreamInfo. | Local native delivery and cancellation regressions passed; frontend/cloud chat validation remains. Pre-connect audio remains unsupported and must be disabled. See [NATIVE_CHAT.md](NATIVE_CHAT.md). |
| Room callbacks / participant APIs | Native adapter returns no Pion callback; AttachRoom/ReconcileParticipants are no-ops. Custom startup sets session.Room=nil and does not connect job.Room. | Pion-based callbacks, participant entrypoints, waits, attributes and direct room access cannot be assumed functional. Audit each actual consumer before broad promotion. Do not create a second Pion connection to supply these. |
| RoomOptions | In-process startup now validates disabled pre-connect audio and maps ParticipantIdentity, DisableTranscriptionOutput and TranscriptionNextOutput. See [NATIVE_ROOM_OPTIONS.md](NATIVE_ROOM_OPTIONS.md). | Other options are not generally mapped; RecordRemoteParticipants now maps auxiliary microphone recording; see [NATIVE_REMOTE_RECORDING.md](NATIVE_REMOTE_RECORDING.md). Core input mute is checked before forwarding/recording frames. |
| Recording/reporting / deletion | RecorderIO is reused for selected input and agent output; report population and explicit room deletion via server API exist. | Current local needs covered by source/cloud evidence. In-process `RecordRemoteParticipants` now reuses the auxiliary recorder for other eligible microphones; local content/isolation and cleanup validation passed. Record-only session mute now retains primary and auxiliary recording, with decoded-recording regression coverage; live downstream transfer validation remains. Cloud/multi-track load validation and app-specific deletion policy remain. |
| SIP/DTMF / room tools | In-process incoming/outgoing DTMF and explicit session-bound send/end-call tools implemented; local native packet exchange, room deletion and cleanup validated under the Go race detector. See [NATIVE_DTMF.md](NATIVE_DTMF.md). | Real SIP trunk/PSTN validation remains. Dialing, transfers and hold are not added. Helper behavior is unchanged; tools require explicit application registration. |
| Generic data/RPC, video/avatar | No general native room facade or implemented application handlers. | Not shown as active local requirements. No support claim; audit separately if selected. RPC/video are not claimed as automatic baseline RoomIO features. |

The pinned FFI schema already includes requests for SetLocalAttributes, PublishData, PublishTranscription, SendText, PublishSipDtmf and RPC. This is API availability evidence only. Our runtime still needs bounded callback routing, cancellation, handle ownership, reconnect/shutdown behavior and tests for each feature actually adopted.

## Evidence and practical limits

- [Latest cloud shutdown checkpoint](../native-receive-probe/checkpoints/cloud-shutdown-20261117/README.md): 100.000 s PCM / 100.000511 s steady wall, max callback gap 42.482 ms; clean session/native shutdown; one resumed false interruption. Separate zero-audio TTS error spans remain unresolved and must not be called a transport failure without diagnosis.
- [Concurrent soak](NATIVE_INPROCESS_SOAK.md): 11 sessions, three lanes, 2700 s aggregate PCM under the Go race detector; resource gates passed. Separate AgentServer fixtures in one process do not prove cloud dispatch concurrency. Identical room tones do not independently exclude audio cross-routing. Rust/C++ was not race-instrumented.
- [Reconnect limitation](NATIVE_RECONNECT_FIX.md) and [Go/Python outage comparison](../native-receive-probe/PYTHON-OUTAGE.md): accepted experimental gap, not a passed uninterrupted-media contract.
- [Normal build and pinned library](packaging/INPROCESS.md): no Python/probe setup build dependency. macOS arm64 runtime validation does not certify all deployment targets or a pristine machine.

No historical artifacts or acceptance gates were changed for this review. No new runtime tests were needed to establish these source-level omissions; their implementation would require regression and real client tests.

## Smallest path toward a default

1. Follow the concrete [downstream audit](../../docs/features/native-downstream-compatibility.md): the six named RoomOptions are not the entire application contract, and disabled pre-connect audio applies only to telephony today.
2. Export an architecture-compliant native API from `interface/worker`. The current examples/internal transport cannot be imported downstream; build tags and example environment flags do not select it in agent-worker-golang.
3. Record-only mute retention is fixed with primary and auxiliary recording regressions; validate the complete downstream transfer/recording-segment workflow after migrating its room APIs. Preserve existing ordinary-recording results and gates.
4. Wire downstream reliable `agent.events`, room lifecycle callbacks, hangup participant enumeration and room identity without connecting Pion. Migrate hold/transfer/supervisor consumers or explicitly exclude those workflows in a restricted native test profile. Reject unsupported non-default RoomOptions.
5. Run combined downstream cloud/client validation, distinct-marker concurrent chat/recording/reconnect, actual worker dispatch and drain/startup-failure tests. Keep native responsible for playout/reconnect and retain restart-based rollback. The separate zero-audio TTS issue remains independent follow-up.

The helper-primary fallback shares this room/playback adapter, so it preserves process isolation but does not restore missing room features. Default Pion remains the broader compatibility fallback. Both remain unchanged; no live transport switching is proposed.

## Source anchors

- [`nativeroom.Room`](../internal/nativeroom/room.go): discovery, selected input, lifecycle, recording and deletion.
- [`launchInProcess`](../internal/nativeroom/inprocess.go): shared runtime and input/output readiness.
- [`playback`](../internal/nativeroom/playback.go): playback/controllers; related `playback_pause_test.go` tests.
- Native [`events.go`](../native-receive-probe/nativeinprocess/events.go), [`reconnect.go`](../native-receive-probe/nativeinprocess/reconnect.go), [`runtime.go`](../native-receive-probe/nativeinprocess/runtime.go): event coverage, exact-SID policy and auto-subscribe.
- Default [`room_io.go`](../../interface/worker/livekit/room_io.go): RoomOptions, state/transcription listeners, text/pre-connect registration, DTMF, microphone-source selection and output subscription wait; [`client_events.go`](../../interface/worker/livekit/client_events.go): client data events.
- [`job.go`](../../interface/worker/job.go): StartSession custom branch and Pion-based participant wait APIs.
- [`native_receive.go`](native_receive.go), [`native_inprocess_enabled.go`](native_inprocess_enabled.go), [`main.go`](main.go): tracked starter selection and application composition. `examples/local` was inspected separately and is not a reproducible tracked fixture.
- [`ffi.pb.go`](../../interface/worker/livekit/ffipb/ffi.pb.go): pinned native request surface.
