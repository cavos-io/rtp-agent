# In-process native remote participant recording

`RoomOptions.RecordRemoteParticipants` now enables auxiliary microphone recording for the in-process native transport. It remains false by default. Enable it in the `RoomOptions` passed to `job.StartSession` alongside `DisablePreConnectAudio: true` and your `ParticipantIdentity` selector. Multiple initial caller candidates still require explicit caller selection.

The selected caller continues through the existing agent input/recording path. Other standard, SIP and connector participants' microphone audio feeds the existing `RecorderIO.RecordAux` mixer only. Agent/ingress/egress/bridge participants, publish-on-behalf participants, screen-share, video and unknown publications are excluded. `DisableAudioInput` prevents auxiliary capture, and session input mute suppresses agent processing while retaining recording delivery. Other RoomOptions not previously implemented remain unsupported.

This matches the default transport's auxiliary recording selection and shared recorder behavior. It adds no agent connection, helper subscriber, new recording format, custom audio playout, or changes to default Pion transport behavior. Helper mode retains its existing behavior. Remote tracks are not injected into STT, VAD, caller captions, or the primary audio hook.

## Downstream audit qualification

A subsequent source audit found that agent-worker-golang uses `SetInputAudioMuted(true)` for transfer **record-only** mode. This was fixed after the audit: native now retains primary and auxiliary recordings under session mute while keeping muted input away from the agent. Decoded-recording regressions reproduce the original failure and pass under the race detector; helper behavior is preserved. The successful live tests below did not exercise transfer itself. See [record-only regression evidence](NATIVE_RECORD_ONLY.md) and the [downstream audit](../../docs/features/native-downstream-compatibility.md#newly-identified-record-only-incompatibility). Earlier results and acceptance gates are unchanged.

## Ownership and bounds

Native decodes each eligible auxiliary microphone to mono 24 kHz / 20 ms frames. The owner tracks native stream/track handles independently from the selected caller. Unsubscribe, unpublish, participant departure and stream EOS release only that auxiliary stream. Replacement tracks can attach again; late events from retired streams are ignored. Native room shutdown releases remaining auxiliary handles before the room, and global runtime teardown also releases them. Native still owns reconnect and playout.

Experimental limits are 32 simultaneous auxiliary tracks and 100 queued auxiliary frames per session, shared across those tracks. Queue saturation, native stream creation/PCM errors or a recording consumer stalled for two seconds fail the affected session visibly. These are resource bounds, not a guarantee of the default transport's capacity. Recorder storage/mixing behavior and its memory model are inherited unchanged.

## Validation (macOS arm64, pinned FFI 0.12.80)

Evidence is in `examples/native-receive-probe/results/native-remote-recording-20261117/`; see [manifest](NATIVE_REMOTE_RECORDING_RESULTS.json).

A local LiveKit test used one native agent connection and two remote microphone publishers, with distinct 440 Hz caller and 1760 Hz auxiliary tones. Two sequential sessions in one Go process tested the option disabled and enabled under the race detector:

| Check | Disabled | Enabled |
|---|---:|---:|
| Primary 20 ms frames | 200 | 200 |
| Auxiliary recorded frames | 0 | 100 |
| Auxiliary marker detected in agent input | 0 | 0 |
| Auxiliary marker peak in decoded final Ogg recording | 72.88 (below 100 limit) | 5288.72 (above 500 minimum) |
| Native handle cleanup | Passed | Passed |

The auxiliary publisher disconnected after two seconds; the primary continued for the remaining two seconds. Final recording files were decoded with system FFmpeg for content verification; FFmpeg is a test inspection tool, not a new runtime dependency. This live fixture exercises the in-process transport and shared recorder directly; RoomOptions wiring is covered separately by regression tests.

Regression coverage includes participant/source filtering, disabled options, input isolation, queue overflow isolation, EOS/republication, canceled delivery, late events, handle release and RoomOptions propagation. Probe/helper and native starter suites passed under the Go race detector; the root repository suite, architecture check and vet passed. The Rust/C++ native library was not race-instrumented.

3,651 prior evidence hashes matched. No earlier successful or failed result was overwritten. Cloud/browser multi-participant recording, long multi-track load and live reconnect-with-auxiliary-recording are not yet validated. Existing native reconnect gap acceptance and other compatibility gaps remain unchanged.
