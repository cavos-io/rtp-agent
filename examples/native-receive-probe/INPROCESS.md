# In-process native room feasibility

2026-11-16: the isolated Go prototype passed concurrent duplex room ownership,
closing one room while the other continues, five repeated sessions, and a
worker-style shutdown with two rooms active. This supports direct FFI loading;
it does not enable it in agent-starter-go or replace the helper transport.

## Boundary and ownership

`inprocess.go` and `inprocess_test.go` are compiled only with
`-tags nativeinprocess` in this nested module. No existing Go implementation,
default transport, helper fallback, dependency pin, or acceptance gate changed.
The code is a feasibility prototype, not a supported public binding.

One Go test process initializes FFI once and owns two simultaneous native room
connections. A single callback dispatcher routes async completions by unique
request ID, room events by room handle, and audio events by stream handle.
Go copies native PCM before releasing frame handles. Per-room disconnect drops
only that room's resources; global dispose runs after all rooms and outstanding
requests finish. The library stays loaded for the process lifetime.

Two Pion publishers in the same test process supply distinct 600 Hz / 1000 Hz
tones. Each native room receives PCM and echoes it using a native audio source.
The corresponding publisher decodes the return track and checks the marker.
Pion is test stimulus/observation here, not the agent's receive engine. There
are no helper subprocesses, Python receivers, or paid provider calls.

This prototype serializes callback handling, including the test PCM consumer.
A slow consumer could delay other rooms. Production integration needs bounded
per-session delivery and explicit failure/cancellation ownership before this
can replace the helper. It must not introduce custom playout timing.

## Gates fixed before the live runs

- After subscription/content startup, accumulated PCM versus callback elapsed
  time differs by no more than 250 ms; no input callback gap exceeds 250 ms.
- Each room receives at least 50 input and output tone-marker frames. Any
  dominant other-room tone is a failure (amplitude >1000 and >3x own tone).
- Room B advances PCM within 250 ms of shared-clock elapsed time across A's
  shutdown and produces at least 50 additional return marker frames.
- Each room disconnect completes within 3 seconds. Worker-style shutdown drains
  room/stream maps and all pending async requests before global dispose.
- Callback overflow is a failure. Final Go-owned goroutines must return to
  baseline; retained foreign-thread callback contexts are classified separately.

PCM duration is samples/24,000. Wall duration is last minus first callback time
plus one 20 ms frame. Survivor measurements use the same process monotonic
clock. These exclude connection startup and do not establish end-to-end voice
latency, sample-perfect output, DTX/loss parity or bit-identical audio.

## Actual results

Race-enabled Go 1.27.4, macOS 27.2.1 ARM64, local LiveKit server 1.13.11.
Native FFI 0.12.80 / rust-sdks
`5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`; native library SHA256
`41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735`.
The Go race detector does not instrument the prebuilt Rust/C++ library.

Both runs passed. Run 02 added settled snapshots and a final Go-owned goroutine
assertion; run 01 is retained. Machine-readable results and source digests:
[INPROCESS-RESULTS.json](INPROCESS-RESULTS.json).

| Run 02 measurement | Result |
|---|---|
| Concurrent room A | 3.560 s PCM / 3.560859 s wall; 21.156 ms maximum gap |
| Concurrent room B | 6.560 s PCM / 6.561864 s wall; 22.132 ms maximum gap |
| B during/after A shutdown | 3.040 s PCM / 3.020952 s wall; 151 additional output marker frames |
| Five repeated sessions | 2.58–2.68 s PCM each; maximum callback gap 24.072 ms |
| Wrong-room output markers | Zero across all nine room connections; no wrong-room input assertion failed |
| Individual room shutdown | 1.186–6.413 ms |
| Shutdown with two active rooms | 9.065 ms including disconnects and global dispose |
| Final ownership | Zero rooms, streams, pending requests, queued callbacks; no callback overflow |
| Final Go-owned goroutines | 2, same as baseline |
| Retained foreign callback contexts | 9, stable across five repeated-session snapshots |
| Repeated-session Go heap after GC | 6.80–7.26 MB; final 7.01 MB (initial baseline 4.01 MB) |

Some 100 ms post-close snapshots still contain Pion publisher teardown
goroutines (39 Go-owned in the first two repeated-session snapshots); later
snapshots and the final one return to 2. Full stacks are preserved. This does
not show one leaked Go receiver per session. Native heap/RSS growth and long
soak behavior were not measured; do not infer native leak freedom from Go heap
or goroutine counts. The native runtime retains threads after dispose as
previously documented in [CYCLES.md](CYCLES.md).

Verification:

- `go test -race -tags nativeinprocess -run 'Test(NativeInProcessRooms|ProcessRequestRouting)$' -count=1 -timeout 120s -v .`: PASS (run 02 package time 26.252 s).
- `go test -race -count=1 ./...`: PASS, existing default probe/helper unit tests.
- `go vet -tags nativeinprocess ./...`: PASS.
- Request-routing regression covers out-of-order completions, duplicate IDs and
  wrong callback kinds. Live tests cover stream isolation and shutdown scope.
- 3,253 earlier result files verified unchanged. No full repository suite claim.

## Reproduce

Use existing pinned probe setup and installed dependencies; see
[README.probe.md](README.probe.md). Do not run this concurrently with another
native-runtime test in the same process.

From the repository root, start the local server in a separate terminal:

```sh
livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml
```

Then:

```sh
cd examples/native-receive-probe
export GOCACHE="$PWD/.cache/go-build"
export LIVEKIT_LIB_PATH="$PWD/.cache/native/liblivekit_ffi.dylib"
export PROBE_INPROCESS_URL=ws://127.0.0.1:18991
# Must be new; existing evidence is never overwritten.
export PROBE_INPROCESS_OUT="$PWD/results/inprocess-new"
go test -race -tags nativeinprocess \
  -run 'Test(NativeInProcessRooms|ProcessRequestRouting)$' \
  -count=1 -timeout 120s -v .
```

This run's raw PCM, callback measurements, goroutine stacks and logs remain in
`results/inprocess-20261116/`. The local server started for this experiment was
stopped afterward.

## Decision and remaining work

Direct native loading is viable for concurrent room ownership on this host.
The helper is not intrinsically required by FFI, but still provides native
crash/hang containment and remains the available agent integration.

Before an opt-in in-process starter path: extract a reusable process-lifetime
runtime with per-room cancellation and bounded delivery; cover stalled
consumers, failed/late connects, simultaneous session cancellation, and shutdown
while requests are in flight. Carry over playback interruption/recovery and
reconnect regression tests. Native crashes would still terminate the worker.

The shutdown test exercises the intended room-drain/dispose sequence in the
Go harness. It does not exercise the actual worker's SIGTERM/job-dispatch hooks.
Five short repeated sessions are not the earlier five two-minute helper soak.
Full agent/provider runs, concurrent load, native memory budgets, deployment
packaging and compatibility gaps remain. Existing reconnect-gap acceptance and
all earlier failed evidence stay unchanged. No production promotion is implied.
