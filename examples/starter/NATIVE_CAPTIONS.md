# In-process native live captions

2026-11-17. Implemented on top of `fdc689de9`; native remains opt-in. FFI 0.12.80, rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, macOS arm64. No dependency or native binary change.

## Behavior

The in-process room now forwards caller interim/final transcripts and agent speech-aligned transcription events over both LiveKit transcription packets and `lk.transcription` text streams. It uses the existing default RoomIO caption implementation for segment IDs, cumulative legacy packets, streamed agent deltas, finalization, replaced speech and interleaved caller/agent captions. Agent final events do not append the whole utterance a second time to a stream. Caller text streams carry the caller track ID; their sender remains the agent, matching default publication.

`CaptionForwarder` constructs only the existing caption state/listeners: no Pion room, codecs, recorder or audio controllers. The native connector supplies publication and stream operations. Output track identity comes from the native publish callback. This keeps the agent and caller as the only two participants. No STT, PCM receive policy, or default RoomIO constructor/behavior changed.

Each caption operation waits at most two seconds for its native acknowledgement. The native actor remains free to process PCM during this wait. At most eight caption requests per session can remain pending, including timed-out calls. New requests fail visibly during reconnect/startup or saturation. There is no unbounded caption replay queue. Session cleanup retires pending caption requests; late completions cannot retain sessions or stop another room. A failed text writer stops sending subsequent chunks and attempts to close with an error reason. UTF-8 chunks are bounded to 12,000 bytes.

Caption listeners and text streams close before native room teardown when the transport remains usable. Shutdown deadlines cancel pending caption writes. Publication failures are logged separately; they do not become TTS errors or stop PCM. Failed caption delivery is not silently declared successful or retried indefinitely. Caption continuity through a full native reconnect is not certified by this test; events during the outage can be unavailable and are not replayed.

## Validation

Source contract: `interface/worker/livekit/room_io.go` at the base revision. Regression tests reuse that caption engine rather than creating a separate segmentation algorithm. This is Go default-transport compatibility, not a new Python parity claim.

| Check | Result |
|---|---|
| Caller interim/final packet IDs and track identity | Passed, unit and live receiver |
| Agent incremental packets, final packet and nonduplicated text stream | Passed, unit and live receiver |
| Interleaved caller caption cuts agent segment; resumed final excludes spoken prefix | Passed, deterministic event regression |
| Caption failures, pending bound, cancellation, reconnect rejection, late callback cleanup | Passed, unit regressions |
| Native wrapper suite under race detector | Passed |
| Focused caption/default-transcription/false-interruption regressions, three repetitions | Passed |
| Default/helper nativeroom and starter suites under race detector | Passed |
| Architecture check and tagged vet | Passed |

Real local LiveKit room, race-instrumented Go starter, injected transcript events and actual native publication; no paid providers:

| Measurement | live-01 | live-02 (after teardown changes) |
|---|---:|---:|
| Participants during / after agent close | 2 / 1 | 2 / 1 |
| Transcription packets / completed streams observed | 5 / 3 | 5 / 3 |
| PCM / measured wall, seconds | 5 / 5.000832 | 5 / 5.000505 |
| Maximum PCM callback gap, ms | 21.623 | 55.499 |
| Output tone markers, frames | 50 / 51 | 50 / 51 |
| Native session cleanup, ms | 3.300 | 3.999 |
| Handles released / worker disposed | yes / yes | yes / yes |

The original PCM/content/cleanup gates remain unchanged. These short runs establish caption wire delivery while the audio harness remains healthy. They do not establish real STT accuracy, acoustic caption alignment, browser rendering, or caption recovery across an outage. Rust/C++ is not race-instrumented. Existing controlled playback recovery results remain separate evidence.

The broader `interface/worker/livekit` suite failed `TestRoomIOOffPlaybackStartedUsesCallbackIdentity`; it also failed three isolated repetitions. That test/default playback implementation was not modified. Do not claim the whole package suite passes. A first sandboxed suite attempt also failed an existing httptest listener with `operation not permitted`; both failed logs are preserved. No unrelated fixes or weakened assertions were introduced.

## Reproduce and cloud follow-up

Build the whole example package using the existing toolchain and pinned native library:

```sh
go build -tags nativeinprocess -o agent-local ./examples/local
# With the existing provider/model configuration:
export RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1
export RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0
export LIVEKIT_LIB_PATH=/absolute/path/to/liblivekit_ffi.dylib
./agent-local start
```

No additional caption flag is needed. The tracked starter can be built instead with `./examples/starter`. Validate both caller and agent captions in the actual client, including interrupted speech/resume and normal departure. An echo load test without a caption-observing client does not prove UI rendering.

For the offline local integration test, start the existing local server configuration and use a **new** result directory:

```sh
LIVEKIT_LIB_PATH=/absolute/path/to/liblivekit_ffi.dylib \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/native-captions-new" \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessNativeCaptions$' -count=1 -timeout 90s -v
```

Evidence: `examples/native-receive-probe/results/native-captions-20261117/`, including `captions.json`, original audio summaries and complete logs. Historical 3441-file hash manifest verified without mismatches. Earlier cloud/results files and failed attempts were preserved. The helper fallback retains its existing behavior; this change does not add captions to helper-primary or promote native to default.

## Frontend confirmation and callback follow-up — 2026-11-17

The user reports testing live captions and seeing them in the frontend. This is user-reported rendering confirmation; no additional cloud archive, screenshot, or timing evidence was supplied with that confirmation. Existing local wire-delivery evidence remains unchanged.

The callback-identity failure above was subsequently reproduced and corrected in both legacy RoomIO playback listener removal methods. They previously compared code addresses, which cannot distinguish closures with different captured channels. The fix uses the existing AgentSession callback-identity compatibility approach. The original failing test remains intact; an additional finished-playback test covers shared-code closures, an unregistered closure and repeated removal. Default transport selection, media policies and helper fallback are unchanged. See [CALLBACK_IDENTITY_FIX.md](CALLBACK_IDENTITY_FIX.md) for verification and scope. Historical failed logs and this report's original results remain preserved.
