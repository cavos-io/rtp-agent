# In-process playback interruption and reconnect compatibility

2026-11-16. The isolated `nativeinprocess` wrapper now supports explicit output
pause/resume/hard-clear and same-track native reconnect. No starter transport
selection, helper fallback, STT adapter or receive playout policy changed.
Prior reports remain historical evidence; this report supplements
[NATIVE-WRAPPER.md](NATIVE-WRAPPER.md).

## Compatibility contract

The authoritative Go compatibility target is the existing helper implementation:
`examples/internal/nativeroom/playback.go` and the probe's
`receive_lifecycle.go`. This task does not claim new Python parity.

`Session.Pause(ctx)` stops new native captures, waits for an already submitted
capture acknowledgement, then clears the native source queue. Already submitted
native-queued audio is discarded; one unsubmitted Capture is retained. A producer
blocked on that Capture resumes in order after `Resume(ctx)`. Native receive and
other sessions continue independently. Input consumption must not share a loop
that blocks on paused output.

`Session.Clear(ctx)` is a hard interruption: it cancels a parked capture, waits
for the in-flight native capture, clears native output, and unpauses. Resuming
cannot bring back the canceled content. New captures can start afterward.
Controls are acknowledged operations; conflicting concurrent controls return
`ErrControlBusy`. Cancellation bounds waiting but does not undo an already
accepted output control. Clear failures remain visible and end the affected
session. Closing releases waiting controls/producers.

This is application output control, not custom receive scheduling, padding,
concealment or time stretching. It does not add the full agent playback adapter's
resampling, playback-position accounting or segment completion interface.

Native `Reconnecting` retains the application session. The old stream/track is
released on reconnect unsubscribe/EOS; late old-stream frames are released and
never delivered. A subsequent subscription must match the original participant
identity and track SID. Native output handles remain owned throughout restart.
A different SID is rejected. Native owns reconnect retries and media recovery.

A 30-second lifecycle bound covers reconnect/resubscription, matching the helper
bound; this is not a packet deadline. Normal publisher departure, unsubscribe or
stream end produces the distinguishable `ErrSourceEnded` cause. Global runtime
shutdown remains separate from room lifecycle.

## Tests

Deterministic regression coverage:

- capture acknowledgement precedes source clear;
- paused capture retained through resume, canceled by hard clear;
- parked-producer cancellation and session closure;
- source-clear failure remains visible;
- reconnect retires only selected input handles, retaining output;
- same-SID rebind routes a new stream; old-stream frame handles are released;
- normal unsubscribe ends the source;
- replacement SID is rejected.

Live evidence uses two independent flows: a continuously active caller supplies
600 Hz input; the wrapper publishes its own output markers. Input is consumed
independently from output. No paid providers or helper subprocess are used.

The test pauses the 600 Hz output producer, observes a quiet marker window after
250 ms of packet/decoder-tail allowance, resumes that same producer, then hard
interrupts and switches to 1000 Hz. This proves the control sequence needed for
false-interruption recovery; it does not run an adaptive detector or the full
agent speech-activity state machine.

For reconnect, only the native participant's signaling passes through a local
reverse proxy. It closes the original WebSocket and returns 503 for two seconds.
RTP stays direct; the caller remains subscribed, published and unmuted. The test
requires a replacement WebSocket and sustained input/output after recovery.

| Live run | Actual result |
|---|---|
| Output markers during paused observation | 0 |
| Resume same producer | 25 further 600 Hz marker frames |
| Hard clear / new output | Old pending capture canceled; 25 new 1000 Hz marker frames |
| Two-second signaling outage | Native resumed after failed resume/full-restart attempts |
| Maximum PCM callback gap across outage | 2,520.544 ms |
| Original 3-second gap gate | Passed this run; earlier accepted failures remain preserved |
| Sustained recovered input | 2.000 s PCM / 2.000641125 s wall |
| Sustained recovered output | 100 marker frames |
| Shutdown | Session and runtime Close completed successfully |

Continuity acceptance outside the outage remains ±250 ms duration error and the
existing wrapper tests retain their 250 ms callback-gap gate. Pause permits at
most two marker frames during the observation window; zero occurred. This run
is not evidence that every reconnect finishes under three seconds. The earlier
accepted ~3–4 second reconnect gap remains a deployment limitation.

Environment: macOS 27.2.1 ARM64, Go 1.27.4, local LiveKit server 1.13.11,
FFI 0.12.80 / rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`.
Native dylib SHA256:
`41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735`.
Go race instrumentation does not cover prebuilt Rust/C++ internals.

Passed:

- Nested module `go test -race -count=1 ./...`.
- `go vet -tags nativeinprocess ./...`.
- Live `TestNativeWrapperCompatibility` under `-race` (10.011 s package time).
- Existing live `TestNativeWrapperLocal` under `-race`, preserving stalled-consumer
  isolation, failed-connect visibility and repeated-session cleanup (9.705 s).
- Helper fallback package `go test -race -count=1 ./examples/internal/nativeroom`
  (12.441 s), including existing playback recovery regressions.

No full-repository suite or Python comparison was run for this change.

## Reproduce

Start the local server with the preserved configuration:

```sh
livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml
```

In another terminal:

```sh
cd examples/native-receive-probe
export GOCACHE="$PWD/.cache/go-build"
LIVEKIT_LIB_PATH="$PWD/.cache/native/liblivekit_ffi.dylib" \
PROBE_INPROCESS_URL=ws://127.0.0.1:18991 \
PROBE_INPROCESS_OUT="$PWD/results/wrapper-compat-new" \
go test -race -tags nativeinprocess \
  -run '^TestNativeWrapperCompatibility$' -count=1 -timeout 60s -v .
```

The output directory must be new. Run each native integration case in a separate
Go test process because the FFI runtime is initialized once per process.
Results, red test log and earlier successful runs remain in
`results/wrapper-compat-20261116/`. The server used here was stopped afterward.
Machine-readable summary and preservation/source hashes:
[NATIVE-WRAPPER-COMPAT-RESULTS.json](NATIVE-WRAPPER-COMPAT-RESULTS.json).

Next integration work remains opt-in starter wiring: bridge these controls to
agent playback accounting/false-interruption hooks, preserve same-track selection,
then repeat actual worker lifecycle and cloud tests. The helper is still the
available agent path; default transport is unchanged.
