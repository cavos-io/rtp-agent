# In-process audio lifecycle and source selection

Implemented 2026-11-17, starting from `bca8974fa`. Opt-in only. Native FFI 0.12.80, rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, macOS arm64. No custom audio playout, silence generation, clock recovery or packet-admission policy. Helper discovery and exact-publication receive semantics remain unchanged; default Pion remains unchanged.

## Behavior

- In-process discovery selects audio explicitly marked MICROPHONE, including muted publications. Screen-share audio, video and unknown-source tracks are excluded. Multiple initial microphone candidates still fail as ambiguous; select the participant explicitly when necessary.
- The native wrapper retains the selected caller across microphone track replacement. Publication metadata identifies source; a new microphone subscription replaces the old input sink, retires its native handles and updates the caption publication track ID. Other participants and screen-share subscriptions cannot replace it. In overlapping replacement, the latest eligible subscription becomes the input.
- Mute/unmute retains the sink and session. WebRTC determines the resulting PCM; this wrapper neither invents silence nor treats mute as DTX.
- Unsubscribe/EOS waits for replacement using the existing 30-second lifecycle bound, rather than immediately ending the in-process job. Missing replacement eventually fails visibly. Publisher departure outside reconnect still ends normally. Existing exact-SID probe API and helper behavior are retained.
- Initial output capture waits for the native local-track-subscribed event. Like default RoomIO, it falls back after 10 seconds and logs the timeout. Cancellation/close ends the wait. The event identifies the output track, not the subscriber; this is evidence that someone subscribed, not remote speaker rendering or selected-caller acknowledgement.

Caption packet/text metadata follows the current microphone SID. Cloud frontend rendering after device replacement has not been independently validated.

## Evidence and gates

The local starter harness runs three sequential sessions in one process under `-race`, with an active 1760 Hz screen-share publisher and a 660 Hz microphone. Agent output carries 990/1320 Hz markers. It uses no paid providers or SIP calls. Go race instrumentation does not instrument Rust/C++.

Existing steady-state gates remain: PCM/wall difference <=250 ms, each one-second window difference <=250 ms, maximum callback gap <=250 ms, both output markers present, input energy present and cleanup settled. New lifecycle gates require capture to remain blocked for 300 ms before subscription, resume within 3 seconds after subscribing, and PCM to resume within 2 seconds after lifecycle setup. Screen-share amplitude >1000 at the distinct marker frequency fails the test; the screen publisher must send at least 25 packets and propagate publishing errors.

The deliberate replacement gap is measured separately before resetting the new scenario's steady-state counters. Existing scenarios retain their original counters and gates. This does not claim uninterrupted media through device changes.

Final run: `results/native-lifecycle-20261117/live-04`.

| Scenario | Steady PCM / wall (s) | Max steady gap (ms) | Native cleanup (ms) | Screen packets |
|---|---:|---:|---:|---:|
| Delayed subscription | 5.000 / 5.000376 | 21.551 | 1.607 | 402 |
| Muted startup; mute/unmute during media | 5.000 / 5.000171 | 26.367 | 2.182 | 374 |
| Changed microphone SID | 5.000 / 5.000778 | 35.306 | 2.047 | 377 |

Delayed first capture waited 318.213 ms. Replacement lifecycle maximum gap was 390.914 ms. All scenarios retained two participants during the session, released native handles on close, and excluded the screen marker. Worker runtime disposal passed at the end of the test. The caller stays connected after each ordinary session close until fixture cleanup.

Preserved attempts:

- `live-01`: muted startup passed. Delayed probe added 20 ms to the later playback-accounting assertion; fixed by clearing that separate probe segment. Replacement's intentional gap was included in steady-state measurement; separated lifecycle and steady windows without changing original gates.
- `live-02`: corrected lifecycle windows passed, screen publication present but no active screen packets yet.
- `live-03`: active screen markers and mute/unmute during streaming passed.
- `live-04`: additionally records screen packet counts and checks publisher errors; final evidence.
- An initial sandboxed adapter unit run could not bind the localhost fixture; the authorized rerun passed. A compile typo in the new TrackUnpublished getter was corrected before live execution.

Regression coverage includes source filtering/helper preservation, native replacement/late unsubscribe, mute retaining the sink, normal departure, bounded rebind, early/duplicate output-subscription events, wait cancellation and close. Full native wrapper and tagged/default adapter suites passed under `-race`; architecture and vet passed. Previously documented broader core-agent race failures remain separate; no full core race-suite success is claimed here.

## Reproduce

Start the local test server using the existing experiment config, then run with a new output directory:

```sh
livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml

LIVEKIT_LIB_PATH=/absolute/path/liblivekit_ffi.dylib \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR=/tmp/native-audio-lifecycle-new \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessNativeAudioLifecycle$' -count=1 -timeout 120s -v
```

This covers signaling-driven device republish, not every browser/device hardware path. Cloud dispatch concurrency, required RoomOptions/room APIs, real telephony and deployment promotion remain separate gates. The accepted reconnect outage gap is not reclassified by this test.
