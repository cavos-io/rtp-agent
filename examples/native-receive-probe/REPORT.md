# Native receive feasibility result

**LiveKit native FFI is viable from Go, but this experiment does not clear every acceptance gate.** It delivers clocked PCM through active silence, verified DTX and a controlled packet gap without custom Go playout. The remaining failures and deployment work prevent a production-readiness claim.

Thirty paired 30-second runs used Python RTC 1.1.21 and native FFI 0.12.80 on macOS ARM64. Both receivers subscribed to the same publisher. Duration below is measured in the common **29-second sustained window**, excluding startup. Latencies are milliseconds; p95 is across ten runs.

| Scenario | Packets sent / silence packets | Go / Python PCM seconds | Second onset p95, Go / Python | Content checks |
|---|---:|---:|---:|---:|
| Continuous silence | 1500 / 500 | 29.000 / 29.000 | 146.60 / 96.28 | 10/10 |
| Verified DTX | 1032 / 33 | 29.000–29.050 / 29.000 | 49.37 / 47.05 | 9/10 |
| Controlled 500ms loss | 1475 / 500 | 29.000 / 29.000 | 74.26 / 77.95 | 10/10 |

All runs produced 8.000–8.050 seconds of PCM in the interior eight-second silence window (one-frame boundary variation). DTX omitted 467 silence frames based on encoder evidence; sequence numbers stayed continuous while RTP timestamps jumped. The loss case deliberately omitted 25 packets while advancing sequence numbers. Independent observer traces verified these distinctions; they are not direct native receiver RTP captures.

Startup p95 was 47–50ms. Callback-gap p95 was approximately 53ms for 50ms application frames. All disconnect callbacks arrived, no Go callback overflow occurred, and maximum measured native shutdown was 28.51ms. These are local-host results, not WAN or long-lived-worker guarantees.

## Failed gates, retained unchanged

- **Continuous-silence relative latency:** Go second-onset p95 exceeded Python by **50.32ms**, against the predeclared maximum of 50ms. This remains a failure, even though the excess is smaller than marker/delivery quantization. One run had Go onset 200.99ms versus Python 108.72ms and a Python delivery gap of 483.32ms. No cause is assigned from duration alone.
- **DTX content:** run `dtx-03` repeated marker 8 in both runtimes. Publisher packet 402 was **453.805ms late**, and Python delivery had a **476.084ms** gap. Go callback cadence remained below 56ms, but continuous PCM did not preserve unique content. This is a shared scheduling-disturbance observation, not proof of a DTX defect or of the original incident's cause.

The recorded data remains intact. No tolerance was loosened and no failed run was replaced. Similar PCM durations do not establish bit-identical parity.

## Recommendation and ownership

Use this narrow Go/CGO wrapper as the next integration spike. The tested native library owns the room, signaling, track subscription, codec handling and WebRTC playout. A native sink cannot attach to a Pion remote track. Future production work must either replace room/media ownership or add a second participant; it belongs in `interface/worker/livekit`, delivering existing core audio frames inward.

No supported Go-native RTC binding was found in the bounded searches. The custom wrapper therefore carries FFI lifecycle/schema maintenance. Pion plus NetEq would preserve the current room, but no maintained Go integration with suitable artifacts was verified. Go SDK v2.19.0 `PCMRemoteTrack` still has a concealment TODO and is not equivalent evidence. No alternative production integration was built.

CGO and a matching native binary are required; WebRTC compilation is not. The tested FFI artifact is ~17MiB, and the combined Go test harness ~41MiB. The harness publisher additionally needs libopus/libopusfile. Published native desktop artifacts cover macOS/Linux/Windows ARM64 and x86-64, but this bootstrap is tested only on macOS ARM64; Linux/glibc packaging and Windows loader support remain unvalidated. Preserve Apache-2.0, WebRTC BSD and bundled third-party notices when distributing binaries.

## Evidence and reproduction

[Commands and design](README.probe.md), [version/checksum manifest](VERSIONS.json), [per-run measurements](MEASUREMENTS.csv), [aggregate gates](SUMMARY.json), and [frozen acceptance contract](ACCEPTANCE.md).

Raw PCM, packet traces and detailed marker/cadence measurements are local under ignored `results/full/`, including `measurements.json` and `SHA256SUMS`. The 30-run dataset used checkpoint `778dcc7ce` with the executable digest in VERSIONS.json. Subsequent review changes add scalar-map-safe handle traversal, stronger cleanup checks and consumer-delay instrumentation; final verification is recorded separately.

Source inspection and live results are distinct. The source shows a 10ms native playout loop; live PCM and marker measurements establish the behavior actually observed. No STT, LLM, TTS or interruption inference ran. Production transport and refs/ remain unchanged, and the original incident is not claimed fixed.

## Final verification and remaining blockers

Three additional race-instrumented runs of the reviewed wrapper (one per scenario) passed the measured continuity, marker, relative-latency and shutdown checks. They do not replace the failed original runs. Go callback-to-consumer p95 was 0.49–0.54ms, maximum 1.41ms. All retained stream/track/room handles reported successful release. See [reviewed measurements](REVIEWED.json).

Immediate post-dispose Go goroutine counts were **6–8 versus baseline 3**. These counts alone do not identify a leak or distinguish native callback/runtime goroutines, but they do **not** establish the planned no-outstanding-goroutines condition. At that checkpoint, native heap/thread leak freedom and long-lived reuse remained unresolved. The isolated [same-process follow-up](CYCLES.md) now records two completed 100-cycle runs and identifies retained foreign-thread callback contexts; native heap leak freedom remains unproven. All owned subprocesses exited, and no race report occurred.

Passed: focused Go tests with `-race`, `go vet`, Python analyzer/cleanup tests, Python syntax checks, repository architecture and staged test-integrity hooks; refused connection, cancellation during connection and repeated empty-room shutdown checks. The root targeted-test hook skips this nested module, so its tests were run directly. The unfiltered repository parity gate was stopped while enumerating 9,434 unrelated cases; no full-repository parity pass is claimed.

Recommendation remains provisional: retain this prototype and validate Linux packaging, broader worker/concurrent-room lifecycles, native resource ownership, and latency/content behavior on a controlled host before proposing any production transport change.

A subsequent [controlled investigation](CONTROLLED.md) reproduced split-marker content and native buffer buildup using publisher-only stalls. All 26 uninjected follow-up trials passed their original gates; the original Go/Python differential outlier remains unreproduced. This does not replace the original failed results or establish their exact host-level trigger.

Synchronized native receive statistics and socket-arrival traces are reported in [ARRIVAL.md](ARRIVAL.md). This follow-up retains the original failures and identifies frame-phase/native-buffer variation plus a separate startup subscription race.
