# In-process native concurrent starter soak

2026-11-16, macOS arm64. PASS under the Go race detector; 922.35 seconds test time. Native FFI 0.12.80, Rust revision 5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb. Native Rust/C++ is not race-instrumented.

One Go process (PID 99070), one shared native runtime, three concurrent lanes: one continuous 900-second room and two lanes each rotating five 180-second rooms. Each session uses actual starter wiring with a local caller; each room has two participants. The fixture creates a separate AgentServer per session, so this is shared-process/native-runtime isolation evidence, not cloud worker dispatch/scheduler coverage. Room replacement briefly reduces concurrency during setup/teardown. Eleven sessions completed, delivering 2700 seconds of PCM in aggregate. No paid providers.

| Measure | Result |
|---|---:|
| Long-lived room PCM / wall | 900 / 900.000220 s |
| Rotating room PCM / wall | 180 / 180.000106–180.000534 s |
| Worst PCM callback gap | 113.342 ms |
| False-interruption recoveries | 3; 2000.308–2001.330 ms |
| Session cleanup | All handles released; no per-room global disposal |
| Global runtime Close | Returned successfully within 5-second context |
| Go race reports / failed assertions | 0 / 0 |

All existing per-second and total PCM gates (250 ms), maximum-gap gate (250 ms), output marker, pause/resume, false-recovery, and cleanup assertions passed. The long-lived room continued through peer publisher disconnects, track unpublishes, normal closes, and replacements. Lifecycle isolation passed; identical tone frequencies across rooms do not independently prove absence of audio cross-routing. No signaling outage was injected in this soak; prior reconnect evidence remains separate. False recovery uses controlled speech-boundary injection, not acoustic VAD/providers.

## Resources

| Resource | Cold | Warm, 60 s | Late active, 900 s | After global Close |
|---|---:|---:|---:|---:|
| Go goroutines | 2 | 343 | 343 | 12 |
| Go heap bytes | 4020056 | 12450184 | 12514672 | 7247232 |
| RSS KiB | 73600 | 235376 | 204944 | 210656 |
| OS threads | 13 | 36 | 38 | 35 |
| Numeric file descriptors | 6 | 241 | 263 | 22 |

Predeclared warm-to-late gates: +32 MiB heap, +30 goroutines, +256 MiB RSS, +30 descriptors, +20 threads. All passed. Peak descriptors were 272 (+31 over warm); this was transient, not the late-active gate. Final goroutines and descriptors fell below warm active levels. RSS fluctuated, peaking at 253216 KiB; no sustained memory growth was observed across this run.

Post-disposal resources exceed the cold baseline. This run establishes bounded behavior over these cycles, not that all native/runtime retention is leak-free. Global native library unloading is deliberately unsupported. Exact attribution of retained threads/descriptors requires stack/descriptor inspection in a separate test.

## Reproduction

Start a local LiveKit server with the existing `examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml` configuration. Then, from the repository root, using a NEW results directory:

```sh
GOCACHE="$PWD/examples/native-receive-probe/.cache/go-build" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/concurrent-soak-new" \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessConcurrentSoak$' -count=1 -timeout 18m -v
```

Requires the pinned probe setup plus local `ps` and `lsof` (resource sampling tested on macOS). The test defaults to 15 minutes. `NATIVE_SOAK_DURATION` supports shorter harness debugging; shorter runs are not equivalent soak evidence.

Raw results: `examples/native-receive-probe/results/concurrent-soak-20261116/`. Acceptance gates and 3379 prior artifact hashes were recorded before interpretation; all prior hashes verified unchanged afterward. Aggregate results and artifact/source hashes are in `NATIVE_INPROCESS_SOAK_RESULTS.json`.

Default/helper race tests passed (cached), and tagged integration `go vet` passed. Only integration harness code changed. Helper fallback and default transport are unchanged. This checkpoint includes the harness, aggregate results, and artifact hashes; raw results remain preserved locally. The 15-minute run was not repeated solely for checkpointing.
