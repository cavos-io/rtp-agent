# Native RTC receive feasibility probe

This is an isolated Go experiment, not a production transport change. Native LiveKit/WebRTC owns receive timing, jitter buffering, concealment and playout. The Go wrapper only connects, subscribes, copies PCM and releases handles. Python is a reference receiver, not a dependency of the Go executable.

## Reproduce (macOS ARM64)

Prerequisites already present on the tested host: Go, CGO/Clang, protoc, protoc-gen-go, libopus and libopusfile/pkg-config, FFmpeg, uv, and livekit-server. Setup does not install system dependencies. Check before installing anything missing.

```sh
cd examples/native-receive-probe
./setup.sh
GOCACHE="$PWD/.cache/go-build" go test -race ./...
.venv/bin/python -m unittest test_analyze.py
.venv/bin/python run.py --repetitions 10 --output results/full
.venv/bin/python analyze.py results/full
```

Use a new result directory for each run. `run.py` starts a loopback-only server on 18991, TCP media on 18992, and UDP media ports 19000–19100. It creates disposable local credentials and unique rooms. No provider adapters or paid APIs run. It signals and waits for its own subprocesses on exit. Sandbox execution needs permission to bind local sockets.

`setup.sh` checks archive digests, generates upstream protobuf definitions into ignored `internal/ffipb`, installs a local Python environment, builds `probe`, and prepares speech PCM. Dependencies live in this nested module; the parent module is used only for `core/audio/model.AudioFrame`.

The speech fixture comes from `github.com/hraban/opus` at `c76ea7e21bf3`, `testdata/speech_8.wav` (10.8 seconds), looped and converted to 20 seconds of mono 48kHz PCM16 with FFmpeg. No fixture audio is redistributed in this experiment. Preserve upstream licensing when redistributing fixtures. Each content second starts with a distinct 250ms tone marker; remaining content is speech. Loss is injected at 24.5–25s, outside markers.

## Ownership and lifecycle

The Go process loads the same `livekit-ffi` library as Python 1.1.21. It owns a separate room participant. Pion is used only by the publisher and independent packet observer. Native sinks cannot attach to Pion remote tracks.

The FFI uses protobuf request/response buffers and asynchronous events. The wrapper copies callback bytes before returning, copies responses before releasing their handles, and copies PCM before dropping owned frame handles. A 4096-event callback queue does not block native callbacks; overflow invalidates the run. The native stream uses `queue_size_frames=0`, matching this exact Python SDK's setting. This is an experimental comparison setting, not a production backpressure recommendation.

Connecting requires the native `ReadyForRoomEventRequest` handshake. Track handles must remain alive for the stream lifetime: native audio streaming watches track-handle disposal. Shutdown closes the stream, disconnects the room, releases retained track/room handles and disposes FFI room/handle state. Process-global native runtimes remain alive. The dynamic library stays loaded until process exit to prevent callbacks into unloaded code.

No general Go RTC SDK is implemented. The prototype supports one active room and one incoming audio track at a time. Unhandled owned event objects are released. Callback queue contents remaining at global disposal are reclaimed by the native runtime. Sequential room reuse is exercised by the isolated [same-process lifecycle probe](CYCLES.md). Concurrent rooms, multiple tracks, publishing, encryption and production worker lifecycle remain outside this prototype.

## Measurement

See `ACCEPTANCE.md` for tolerances fixed before results. Output files contain real decoded PCM, callback-delivery sample counts, publisher encoder/packet traces, and independent subscriber RTP observations. `measurements.json` includes per-run values and aggregate p95 comparisons.

All processes explicitly call the same host `clock_gettime(CLOCK_MONOTONIC)` clock. First-frame latency begins at subscription; native receivers can emit silence before publisher RTP starts. Speech onset uses identifiable marker delivery versus actual packet emission. Go timestamps native callback entry; Python timestamps iterator delivery, so wrapper scheduling contributes to their difference. Tone detection uses 20ms windows and 10ms hops, with up to one 50ms frame of delivery quantization.

The packet observer is a different subscription. It proves sender/sparse/loss scenario construction, not every packet received by native WebRTC. Marker survival establishes selected content identity/order; it does not establish bit-identical output or complete speech intelligibility. Source inspection, wrapper tests, and live experiments are separate evidence levels.

## Versions and deployment

- Go publisher: server-sdk-go v2.17.0, Pion v4.2.18; complete graph in go.mod/go.sum.
- Python RTC 1.1.21; full Python pins in requirements.txt.
- Native FFI 0.12.80, Rust SDK commit `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`.
- macOS ARM64 archive SHA-256: `980ae8b1dc687427235ce8de503068d77c6461d383beacb94592eeb2b5595386`.
- Test host: macOS 27.0.1 ARM64, Go 1.27.4, Python 3.14.4, LiveKit server 1.13.11, libopus 1.6.1.

Native release assets exist for macOS, Linux and Windows ARM64/x86-64 plus mobile platforms. Only macOS ARM64 bootstrap/runtime is tested here. Linux requires glibc; the Python distribution explicitly excludes musl. Windows needs loader adaptation because this wrapper currently uses dlopen. Native binary deployment also needs loader paths, correct architecture, and platform minimum versions (macOS ARM64 11+).

Distribute the native artifact's LICENSE.md and applicable Apache-2.0/WebRTC BSD/third-party notices. This does not make the internal FFI a supported Go API: schema and binary must move together, upgrades need lifecycle and audio measurements, and the wrapper is maintained by RTP Agent.

A future integration belongs in `interface/worker/livekit`, delivering provider-neutral audio frames to core. Native RTC must either replace Pion's room/media ownership or connect as a second participant. Worker control-plane/job ownership is a separate integration decision; this experiment does not change it.

Additional checks while the test server is running:

```sh
.venv/bin/python lifecycle.py
```

This checks a refused connection, cancellation during a stalled WebSocket handshake, and three independent empty-room connect/cancel cycles. `PROBE_BINARY` and `PROBE_LIFECYCLE_OUT` select a separate race-instrumented binary and result directory. `run.py --binary .cache/probe-reviewed` can exercise a separately built binary without replacing a running experiment's executable.

The native artifact is approximately 17 MiB; the combined Go publisher/observer/receiver harness is approximately 41 MiB. Its macOS dynamic dependencies are system libraries/frameworks; no separately built WebRTC is needed. The pinned source declares libwebrtc 0.3.49, webrtc-sys 0.3.46 and prebuilt WebRTC tag `webrtc-89d790b`.

The combined harness also links Homebrew libopus/libopusfile for its publisher. Those are harness dependencies; the native receiver uses the codecs bundled in LiveKit FFI. No custom Opus receive decoder is used.

## Upstream evidence

- [Python RTC 1.1.21](https://github.com/livekit/python-sdks/tree/rtc-v1.1.21/livekit-rtc) pins the Rust SDK revision used here. Its `audio_stream.py` explicitly requests an unbounded native queue.
- [Pinned FFI C ABI](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/src/cabi.rs) defines initialize/request/drop/dispose and callback memory lifetime. The shipped header lists only request/drop, so those two declarations alone are insufficient.
- [Pinned native stream](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/libwebrtc/src/native/audio_stream.rs) attaches a sink to a native audio track. [Synthetic audio device](https://github.com/livekit/rust-sdks/blob/5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/webrtc-sys/src/synthetic_audio_device.cpp) requests playout every 10ms. These are source facts, not by themselves continuity measurements.
- [Go SDK v2.19.0 PCMRemoteTrack](https://github.com/livekit/server-sdk-go/blob/v2.19.0/pkg/media/pcmremotetrack.go) still runs RTP packets through a decoder/writer and retains `TODO: handle concealment`.
- GitHub repository searches `neteq language:Go` and `livekit ffi language:Go` returned zero matches during this investigation. Broader NetEq results included TaoistKing/AudioPlc (last push 2021), zhougang123456/AudioNeteq-Cmake (2023), and StephensCheng/neteq (2023). This is bounded discovery evidence, not proof that no binding exists anywhere.
- Inspected NetEq headers expose `SetCodecs`, `InsertPacket` with RTP headers/arrival timing, and `GetAudio` for clocked decoded output. No verified maintained Go wrapper or packaged standalone receive engine emerged from that search. Local CLI WebRTC/APM headers are not a usable NetEq integration by themselves.

The reference framework checkout was `ff962a019b6500e08f8f1fcd7f55673361bd9e91`; its room input calls `rtc.AudioStream.from_track` and defaults to 24kHz/50ms. No framework STT/LLM/TTS was started. The previously inspected Rust revision `81d3f267...` and Python 1.1.13 are not used for this experiment.

Resource evidence is bounded: the harness observes disconnect completion, native disposal returning, receiver process exit, and race-detector results. It does not expose the Rust handle table or establish absence of native heap/thread leaks in a long-lived worker. Production adoption still needs that audit, bounded backpressure design, Linux packaging validation, and worker/room lifecycle integration.

Post-dispose goroutine counts and 100-cycle same-process connect/disconnect results are documented in [CYCLES.md](CYCLES.md), including the retained failed plateau check and native-thread evidence.

The remaining content and latency failures were investigated with controlled sender stalls and native statistics: [CONTROLLED.md](CONTROLLED.md). Original failed results and thresholds remain unchanged.

Synchronized native receive statistics and socket-arrival traces are reported in [ARRIVAL.md](ARRIVAL.md). This follow-up retains the original failures and identifies frame-phase/native-buffer variation plus a separate startup subscription race.

The runner now waits for both native track subscriptions before sending fixture packets. The controlled baseline/stall rerun and preserved startup failure are documented in [BARRIER.md](BARRIER.md).

Post-stall native-buffer and PCM-marker recovery measurements from the preserved barrier captures are documented in [RECOVERY.md](RECOVERY.md), including unconfirmed recovery bounds.

The optional `-mode helper` serves live PCM to the starter over a local pipe, reusing the same native receive engine. See [starter integration commands and limitations](../starter/NATIVE_RECEIVE.md). Normal probe modes and existing evidence remain separate.
