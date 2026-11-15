# Single native room: isolated bidirectional prototype

**Feasible and locally demonstrated: caller plus one native agent participant.**
The Go parent consumes received PCM and supplies outgoing PCM over pipes. The
helper owns one LiveKit native room, its receive stream, local audio source and
published output track. There is no Pion agent-room connection. Pion is used only
by the test caller. RoomService HTTP participant queries add no participant.

This is explicitly selected with `-mode duplex-helper`. Existing helper mode,
agent-starter-go, examples/local, production transport and provider adapters are
unchanged. **The current agent-starter-go load-test configuration still has its
separate native subscriber.** This prototype is not a drop-in two-participant
starter or a production transport migration.

## Verified APIs and ownership

Pinned FFI 0.12.80, rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`:

- NewAudioSource(AUDIO_SOURCE_NATIVE), 24kHz mono, native queue 200ms;
  CreateAudioTrack and PublishTrack on the connected local participant.
- CaptureAudioFrame accepts PCM with an async completion callback. The pinned
  livekit-ffi/src/server/audio_source.rs copies input with `to_vec()` before the
  synchronous request returns. The wrapper uses C-owned input storage and frees
  it after that return. Capture completion is not remote playout completion.
- ClearAudioBuffer delegates to the native source queue. No Go jitter, silence,
  concealment, drift correction or receive pacing is introduced.
- Existing native receive AudioStream produces 24kHz mono 50ms PCM. The application
  PCM rate remains distinct from Opus's 48kHz RTP clock.
- Local participant, source and output track handles remain retained until room
  disconnect, then are released before global FFI disposal. Existing receive
  cleanup and callback ownership logic is reused. Child processes are reaped.

Python RTC 1.1.21 AudioSource and the vendored voice/room_io/_output.py were
inspected: clear_queue maps to the same native API, while Python tracks queued
output duration and playout completion at SDK/application level. No Python
receiver was run for this prototype; no output parity claim is made.

## Protocol

The first stdin line is JSON with URL, Token, Participant and TrackSID. Credentials
travel through stdin, never argv. Stdout starts with duplex_protocol version 1,
then reports input subscribed, output_published, copied PCM and control events.

Subsequent JSON lines have event, id and optional base 64 data:

- capture: exactly 2400 bytes of PCM16LE (50ms,24kHz,mono); await captured.
- clear: await all captures first, then await cleared.
- stop: disconnect, release handles and exit after shutdown reporting.

IDs must increase and start at 1000; FFI connect/disconnect/publish reserve 1–3.
Only one capture may be in flight. This explicit small prototype contract prevents
pending capture work from refilling a cleared queue. It is IPC serialization,
not a custom media scheduling policy. No drain/playhead API is claimed.

## Local evidence

Tested on macOS 27.2.1 ARM64, Go 1.27.4, LiveKit server 1.13.11. Both Go parent and
helper were built with race instrumentation; native-library internals are outside
Go race coverage. Caller SDK server-sdk-go v2.17.0. No dependencies were installed.

Before trials, fixed gates required exactly 2 participants at three server queries,
caller alone after shutdown, input PCM within 250ms of wall and max callback gap
250ms, tone content in both directions, output onset<=500ms, old output cessation
within 400ms of clear, and helper cleanup<=5s. A supplemental queued-clear check
required 200ms of source input to complete capture in<=150ms before clearing.
The pilot was preserved separately, not rescored with that supplemental check.

| Trial | Participants | Native input PCM / wall seconds | Input max gap ms | Outbound packets / sequence gap events | First / second output onset ms | Last old tone after clear ms | Cleanup ms |
|---|---|---|---|---|---|---|---|
| 1 | 2 | 6.050 / 6.021 | 53.120 | 313 / 0 | 16.671 / 5.410 | 6.880 | 1027.445 |
| 2 | 2 | 6.000 / 6.015 | 52.419 | 312 / 0 | 16.671 / 5.947 | 7.379 | 1013.552 |
| 3 | 2 | 6.000 / 6.010 | 73.019 | 312 / 0 | 19.737 / 13.807 | 14.105 | 1028.492 |

All three passed. Input 660Hz and output 990Hz/1320Hz markers were detected. Queued
200ms bursts completed capture in 3.446–7.035ms before clear; old output ceased
and the second marker remained present. A final run after harness failure-cleanup
hardening also passed. Every successful helper exited; only the caller remained
in the room. Existing default/native/track-unpublish starter smoke tests passed
with the same new binary in ordinary helper mode.

Onset uses only the parent process clock, from source submission to packet-decoded
PCM at the caller. **It is not remote playout, acoustic or conversational latency.**
The caller decoder is observation-only, not a proposed receive engine. RTP
sequence gaps were zero; timestamp deltas (including nonuniform values) are
preserved in raw summaries. This is a short continuous-tone experiment, not
speech fidelity, DTX/loss, long-duration or full-provider validation.

Raw results, parent-clock spectral observations, helper stderr, contracts,
source/binary versions and preservation hashes: results/duplex-native/.
All 2948 prior artifact hashes verified unchanged. No earlier results were edited.
Unit protocol tests, nested-module race tests and vet passed. No race reports
appeared in the live trials. CGO, the pinned native library, generated FFI schema,
libopus and the local server are required as in README.probe.md. Deployment must
retain native/WebRTC and third-party license notices; this adds no new dependency.
Only macOS ARM64 was live-tested here; other packaged platforms remain unverified.

## Run

From the repo root, use the localhost server configuration in
../starter/NATIVE_RECEIVE.md (ports 18991/18992, test key devkey and test secret).
The existing probe setup must already supply the pinned library/schema.

```sh
go -C examples/native-receive-probe build -race -o .cache/probe-duplex .
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
DUPLEX_HELPER="$PWD/examples/native-receive-probe/.cache/probe-duplex" \
DUPLEX_TEST_URL=ws://127.0.0.1:18991 \
DUPLEX_RESULT_DIR="$PWD/examples/native-receive-probe/results/duplex-native/manual-01" \
go -C examples/native-receive-probe test -race -tags duplexintegration \
  -run '^TestSingleNativeRoomDuplex$' -timeout 45s -count=1 -v
```

Choose a new result directory each time; the harness refuses to overwrite one.
The test requires no STT, LLM, TTS or cloud credentials.

## Boundary required before agent-starter-go can use two participants

The native media APIs are available. Primary-job integration is the remaining
work, rather than removing the helper process or reusing duplicate identities.

| Current ownership | Needed native primary boundary |
|---|---|
| Worker registration/job assignment | Retain worker control connection; transfer the assigned room token/identity to the native helper instead of joining a Pion media room. Native use of a dispatched job token has not been cloud-tested. |
| JobContext.ConnectPreparedRoom, Room *SDKRoom | Native room lifecycle/participant state must satisfy job readiness and shutdown without calling Pion Connect. |
| JobContext.StartSession / SessionRoomIO | Currently constructs Pion RoomIO and requires GetCallback/AttachRoom(*SDKRoom). Introduce an opt-in transport initialization boundary; this prototype does not alter that contract. |
| RoomIO.PublishAudio and playback controller | Route PCM to capture; map clear/flush/drain, playback position and interruption completion. Native capture acknowledgement alone cannot implement WaitForPlayout correctly. |
| Participant/track selection and reconnection | Translate native events, preserve exact caller selection, subscription barriers and disconnect semantics. |
| Recording, transcription/data, reports | Feed Go PCM recording and implement the native publish/event equivalents needed by the starter; keep provider and interruption decisions in Go. |

Recommended next implementation: a separate opt-in primary native transport
adapter with those lifecycle and playback contracts, followed by provider-free
AgentSession integration tests and repeated two-minute sessions. Only then test
lk perf against a dispatched cloud agent. No changes to production transport or
custom receive policies are required for this prototype, and none were made.
