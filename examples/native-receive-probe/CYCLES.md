# Post-dispose goroutine investigation

The excess goroutines are **foreign-thread callback contexts retained by Go**, not
Go receive loops waiting for frames. They persist because the pinned native FFI
keeps its process-global Tokio runtimes alive after `dispose`.

## Live results

Race-instrumented Go 1.27.4, macOS 27.0.1 ARM64, LiveKit FFI 0.12.80,
Python RTC 1.1.21 publisher, LiveKit server 1.13.11. Each row uses one Go PID.
Baseline is two Go test goroutines; the earlier CLI baseline included signal
handling and was three. Counts below are 100ms after each teardown.

| Experiment | Completed cycles | Go-owned baseline / final | Foreign contexts | PCM samples/cycle, 24kHz | Shutdown range |
|---|---:|---:|---:|---:|---:|
| Initialize once, dispose at end | 100 | 2 / 2 | 5–7; final 7 | 19,200–22,800 | 1.27–9.79ms |
| Dispose/reinitialize each cycle, thread instrumentation | 100 | 2 / 2 | 6–7; final 7 | 16,800–22,800 | 1.17–46.96ms |
| Initial dispose/reinitialize attempt | 58, then failed | 2 / 2 at stop | 6 → 7 at cycle 58 | PCM received every cycle | No final wait |

Both completed 100-cycle runs retained **the same two Go-owned goroutine IDs**,
not merely the same count. Every settled callback queue was empty; every
retained stream/track/room release and disconnect callback succeeded. No race
report occurred. Five seconds after final disposal, both still reported nine
goroutines: two Go-owned plus seven foreign callback contexts.

The initial attempt **failed the predeclared plateau gate**: after 20 warmup
cycles with six foreign contexts, a seventh appeared at cycle 58. Its raw
stacks are retained. That attempt lacked thread-ID instrumentation, so the
new context's native-thread identity cannot be recovered. This failure is not
replaced by the successful rerun. The fixed-20-cycle criterion remains in the
test, but is not by itself a reliable leak diagnosis.

The instrumented run directly observed lazy callback-context creation: Go ID
87 first appeared after cycle 2 on native thread **113376448**, already present
after cycle 1. All seven final foreign contexts mapped to native threads that
existed after cycle 1 and remained alive after final disposal. OS-thread counts
were **39–42** at settled snapshots and **33** five seconds after disposal,
with no per-cycle increase. This supports persistent native runtime behavior,
not one leaked receiver goroutine per connection. Thread enumeration is macOS
only; names were unavailable, so individual pool ownership is inferred from
pinned source rather than asserted from thread names.

[Machine-readable measurements](CYCLES-RESULTS.json) include PIDs, binary hashes,
counts and thread mappings. Raw stacks, PCM, events and logs remain locally in
ignored `results/cycles-process-100`, `results/cycles-dispose-100` (failed), and
`results/cycles-dispose-threads`. Instrumentation adds callback overhead;
these runs test lifecycle/resource ownership, not audio latency parity.

## Mechanism

Every excess stack has exactly this shape:

```text
goroutine ... [syscall, locked to thread]:
runtime.goexit({})
    .../runtime/asm_arm64.s:1039
```

Go 1.27.4 creates this synthetic stack in `$GOROOT/src/runtime/proc.go`,
`oneNewExtraM` (around line 2523). It is the context used when a non-Go thread
calls Go. The `dropm` documentation (around line 2568) explains that on pthread
platforms, it is retained until the foreign thread exits. It is not a Go
function blocked waiting to finish.

In Rust SDK revision `5a656c4...` (full pin below), the FFI server is a
`lazy_static` singleton. Its `FfiServer::default` creates a multithread Tokio
runtime and a dedicated audio runtime. `dispose` closes rooms, clears callback
configuration, watchers and handles; it **does not drop those runtimes**. Their
persistent threads explain the persistent Go callback contexts. Merely waiting
or forcing Go GC is not an appropriate remedy.

Authoritative pinned source:

- [FFI singleton](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/lib.rs#L49)
- [Runtime construction and disposal](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/server/mod.rs#L102)
- Local Go source: `go1.27.4/src/runtime/proc.go`, `oneNewExtraM`, `needm`, `dropm`.

## Reproduce

Use the existing pinned setup from [README.probe.md](README.probe.md). Then:

```sh
GOCACHE="$PWD/.cache/go-build" go test -race ./...
GOCACHE="$PWD/.cache/go-build" go test -c -race -tags nativecycles -o .cache/cycles.test .
.venv/bin/python cycles.py --cycles 100 --lifetime process --output results/cycles-process-100
.venv/bin/python cycles.py --cycles 100 --lifetime cycle --output results/cycles-dispose-100
```

Each command uses **one Go receiver process for all its cycles**, with one real
continuously published tone track. A separate Python RTC process supplies that
track. Both use the existing FFI 0.12.80 artifact on macOS ARM64. The process
experiment initializes FFI once and disposes at the end; the cycle experiment
reinitializes/disposes between rooms as well. Full stack dumps and JSON snapshots
are saved immediately and 100ms after each cycle, then immediately/1s/5s after
final disposal. Default unit tests exclude network/native lifecycle work via the `nativecycles` build tag.

The [contract](CYCLE-CONTRACT.md) requires PCM every cycle, successful teardown,
empty callback queues, unchanged Go-owned stack count, and no foreign-context
count growth after 20 warmup cycles. The classifier deliberately does not ignore
arbitrary locked-thread/syscall/cgo stacks.

A plateau violation fails the test while allowing later diagnostic snapshots;
it does not silently redefine the acceptance criterion. OS thread counts
include Go, WebRTC and native runtime threads; totals alone do not assign
ownership. Native thread snapshots require macOS.

## Integration consequence

Treat the native library/runtime as process-scoped; disconnect rooms separately.
Keep the library loaded rather than attempting `dlclose` while native threads may
still exist. The isolated wrapper now exposes that separation and avoids adding
a new `dlopen` reference on each connection. The repeated-load reference count
was a harness concern, not the cause of the observed goroutine stacks.

This is bounded evidence about goroutine ownership and sequential room teardown,
not proof against all native heap leaks, concurrent-room bugs, or other platform
behavior. It does not change the earlier PCM content/latency failures, and it
makes no production transport change.

## Verification

Final harness: `go test -race -count=1 ./...`, `go vet ./...`, Python analyzer
and cleanup tests (3 tests), and Python syntax checks passed. The final race
binary also passed five same-process room cycles after the last loader-error
and fallback-disposal guards were added; its digest and results are in
`CYCLES-RESULTS.json`. The 100-cycle run hashes identify the earlier test
binaries, rather than implying the final smoke binary ran those experiments.
Default Go tests exclude the opt-in local integration test. No full-repository
parity or cross-platform leak-freedom claim is made.

The repository test-integrity hook rejects `t.Skip`. The local integration test
therefore uses `-tags nativecycles`, with missing fixture configuration treated
as an error. A further five-cycle race run of this final tagged binary passed
(same two Go-owned IDs, empty queues, PCM every cycle). It ended with five
foreign contexts; counts vary with which native threads have called Go. Its
settled OS-thread range was 39–76 and final count 30; one cycle received only
one 50ms frame within the one-second connection window. This short smoke run
is not a startup-latency or native-thread plateau guarantee. Its full summary
is retained alongside the 100-cycle evidence.
