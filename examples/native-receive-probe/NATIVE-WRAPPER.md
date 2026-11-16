# Reusable in-process native wrapper and thread-safety review

2026-11-16. Experimental package: `native-receive-probe/nativeinprocess` in
this isolated nested module. No production transport or helper implementation
was changed. Earlier prototype and failed results are preserved.

## Is the native library thread-safe?

The pinned source provides concrete evidence of a concurrency-capable FFI.
Rust alone would not establish that: this API crosses unsafe pointers, foreign
callbacks and C++ WebRTC. No blanket race-freedom claim is made.

Reviewed LiveKit FFI **0.12.80**, rust-sdks commit
`5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`:

- [FFI callback contract](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/cabi.rs): explicitly states the C callback **must be thread-safe and not block**. Serialized callback bytes are valid only during the callback, so the wrapper copies them before returning.
- [Server synchronization](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/server/mod.rs): stored handles implement `Send + Sync`; handle storage uses `DashMap`; IDs use `AtomicU64`; configuration uses a mutex; callbacks use `Arc<dyn Fn(FfiEvent) + Send + Sync>`. Rooms execute on a multithreaded Tokio runtime, with a separate audio runtime.
- [C ABI request/handle ownership](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/cabi.rs): the caller must supply valid pointers; response storage belongs to a returned handle. The wrapper copies the response before dropping that handle. Rust's internal locks do not protect a foreign pointer used after its handle is dropped.
- [Capture ownership](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/server/audio_source.rs): capture copies samples using `to_vec()` before scheduling async work. The wrapper can free its temporary native buffer after the request returns, but waits for the capture callback before allowing another capture on that session.
- Global setup replaces configuration; global dispose closes rooms and clears handles/configuration. Neither is a per-session operation. A race between disposal and a caller reading response memory is not made safe by `Send + Sync`.

Conclusion: multiple concurrent rooms are supported by the synchronization
model and our live evidence. Initialization, disposal, callback routing,
per-source operation ordering and handle lifetime still require a binding
contract. This wrapper serializes native requests and ownership mutations on
one Go goroutine, while native rooms/media continue concurrently. It does not
need to assume arbitrary concurrent FFI calls are safe. The Go race detector
cannot prove Rust/C++ internals safe; native TSAN was not run.

## Wrapper contract

```go
rt, err := nativeinprocess.Open(nativeLibraryPath) // once per worker process
// Handle err.
session, err := rt.Connect(sessionContext, url, token)
// Handle err. sessionContext controls the whole session lifetime.
err = session.WaitReady(startupContext) // native output publication complete
// Frames arrive independently when the remote audio track is subscribed.
for frame := range session.Frames() {
    // frame owns Go PCM16 memory, mono at 24 kHz.
    // Deliver it to the consumer. Capture is a separate output API.
    _ = frame
}
err = session.Err()
err = session.Close(shutdownContext)
err = rt.Close(workerShutdownContext)
```

This sketch shows ownership, not complete application error handling. In an
agent, receive and output run concurrently. `Capture(ctx, pcm)` copies input and
waits for native acknowledgement. Only one capture per session is allowed in
flight; concurrent submissions receive `ErrCaptureBusy`. Canceling the wait
does not undo a request already submitted to native.

- The FFI runtime is claimed once per process. Do not mix this package with the
  old probe's FFI initializer or another SDK instance in the same process.
  A second `Open` is rejected, including after disposal; the library stays loaded.
- One owner goroutine routes callbacks using unique request IDs, room handles
  and stream handles. No user callback runs on it.
- Each session gets **25 frames of bounded PCM delivery**, normally 500 ms at
  native 20 ms frame size. A full queue fails only that session with
  `ErrBackpressure` and disconnects it. It does not silently drop, pad, retime,
  conceal or adapt audio. Already queued frames remain readable until drained.
- The global copied-callback queue is bounded to 4096 events. Saturation or
  malformed callbacks fail the runtime visibly and trigger global disposal.
- Session cancellation before a connect callback does not abandon ownership:
  a late successful room is disconnected and released. Failed connects terminate
  only their session. Per-session `Close` is safe for concurrent callers.
- Worker shutdown rejects new work, cancels sessions, drains disconnect and
  pending capture callbacks, then disposes globally. `Close(ctx)` bounds the
  caller's wait; it cannot force-kill a hung native call or promise native cleanup
  completed after a deadline. The helper remains stronger crash/hang containment.

This package still uses the pinned generated FFI schema and CGO/dlopen bridge.
No new dependencies were installed. It is reusable within the probe module;
it is not yet imported into the root agent module or a supported SDK release.

## Tests and actual evidence

Environment: macOS 27.2.1 ARM64, Go 1.27.4, LiveKit server 1.13.11.
FFI dylib SHA256:
`41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735`.

Deterministic tests cover burst ordering/queue bounds, stalled-consumer
isolation, cancellation, late successful connect cleanup, failed-connect
isolation, shutdown while connect is pending, malformed callback failure,
canceled capture rejection, concurrent capture rejection and late capture
completion after session teardown. These fake-backend tests prove Go ownership
logic, not native receive behavior.

Live local tests used two distinct tone publishers and native duplex rooms
in one Go process under `-race`. The stalled session had no consumer. The healthy
session consumed PCM and returned it through native capture. Gates remained:
250 ms maximum callback gap / duration error, visible 25-frame saturation,
correct marker content, and bounded test shutdown. Both live runs passed.

| Live run 02 | Actual result |
|---|---|
| Stalled receiver | `ErrBackpressure`, exactly 25 queued frames, session terminated |
| Concurrent healthy receiver | 2.000 s PCM / 2.000523 s wall after stalled session ended |
| Healthy maximum callback gap | 22.971 ms |
| Healthy output markers at observation | 125 |
| Real invalid-token connect | Failure visible; other session remained usable |
| Three repeated sessions | 1.04, 1.08, 1.08 s PCM; 51 returned marker frames each |
| Concurrent Close callers | Both joined the same successful session cleanup |
| Runtime shutdown with active session | 6.797 ms (including test publisher cleanup) |
| Second owner / connect after shutdown | Rejected |

Logs and raw summaries: `results/wrapper-20261116/`. The first red compile test
and failed proto2 fixture attempt are retained; the fixture was corrected to
supply required metadata without changing acceptance. No earlier run was
replaced. Machine-readable evidence: `NATIVE-WRAPPER-RESULTS.json`.

From this directory, with pinned setup already available and local server on
18991 (see `results/reconnect-trace-20261116/server.yaml`):

```sh
export GOCACHE="$PWD/.cache/go-build"
go test -race -count=1 ./...
go vet ./...
LIVEKIT_LIB_PATH="$PWD/.cache/native/liblivekit_ffi.dylib" \
PROBE_INPROCESS_URL=ws://127.0.0.1:18991 \
PROBE_INPROCESS_OUT="$PWD/results/wrapper-new" \
go test -race -tags nativeinprocess -run '^TestNativeWrapperLocal$' \
  -count=1 -timeout 70s -v .
```

Use a new output directory. Run native integration cases in separate test
processes: do not initialize the old prototype and this wrapper in one process.
Ordinary package tests do not load the library or contact a room.

## Remaining limitations

One input audio track per room, fixed mono 24 kHz PCM, no seamless track
replacement/reconnect integration, no pause/clear playback contract yet. Those
must be ported with existing regression gates before starter integration.
`WaitReady` means output publication, not inbound subscription. No input source
selection among multiple speakers is exposed yet.

This is short local lifecycle/queue proof, not the two-minute cloud test,
long-running native memory verification, production worker SIGTERM validation,
or general feature parity. The helper fallback and Pion default remain intact.
No custom playout behavior was added.
