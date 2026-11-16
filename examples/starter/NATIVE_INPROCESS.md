# Opt-in in-process native starter

Implemented 2026-11-16. `agent-starter-go` can use one native room connection in
the Go worker process for both input and output. This requires **both** the
`nativeinprocess` build tag and `RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1`.
Neither the default Pion path nor the helper fallback is replaced.

## Build and cloud test

From the repository root, using normal Go/CGO and the existing agent system dependencies. The pinned FFI Go bindings are included in source; no probe setup, Python, protoc or downloaded SDK source is needed to build.

```sh
go build -tags nativeinprocess \
  -o agent-starter-go ./examples/starter

export LIVEKIT_AGENT_NAME=agent-starter-go
export RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1
export LIVEKIT_LIB_PATH="/absolute/path/to/liblivekit_ffi.dylib"
./agent-starter-go start
```

In another terminal configured for the same LiveKit deployment:

```sh
lk perf agent-load-test --rooms 1 --agent-name "agent-starter-go" --duration 2m
```

`RTP_AGENT_NATIVE_HELPER` is ignored in this mode and need not be removed from
your environment. The three experimental modes are mutually exclusive; setting
two flags to `1` fails startup. Without the build tag, requesting in-process mode
fails with a clear build instruction. Compiling with the tag alone does not
select native transport.

Normal LiveKit/inference credentials, ONNX runtime and Silero model settings
still apply. No paid provider test was run here. This checkpoint includes `examples/starter`; the Git-ignored `examples/local`
workspace also has equivalent opt-in wiring. Run the whole example package,
not its main.go file alone.

The root `go.mod` resolves the experimental wrapper from the included nested Go
module. Keep that source directory in the checkout; its `.cache`, generated
`internal` directory and Python environment are not build prerequisites.
The library is loaded at runtime; it is not needed for compilation or linking.
See [normal build and library installation](packaging/INPROCESS.md) for the
pinned macOS arm64 download and helper rollback commands. The default starter
dependency graph excludes the in-process FFI package.

## Playback and lifecycle integration

The example room boundary selects an in-process connector but reuses the existing
`nativeroom` playback implementation. It keeps the existing resampler, serialized
producer, capture acknowledgement ordering, playback-position accounting,
interrupted-segment results, recording, `WaitForPlayout`, and audio-output
pause/resume hooks. The adapter sends Capture/Clear directly to the wrapper;
no PCM is serialized through a helper pipe.

False-interruption recovery uses the real core agent activity/speech state
machine and the existing output controller. Pause discards native-queued output
and retains unsubmitted content in the producer. Resume continues that speech;
hard interruption invalidates old content. No receive playout policy was added.

Caller discovery and the worker-assigned primary room token are shared with the
helper path. `ConnectSelected` pins the discovered participant and track SID;
other tracks cannot satisfy readiness. Startup waits for both output publication
and the selected input sink, without requiring the first RTP packet. Input mute,
recording and report population retain the existing example integration behavior.

One runtime is lazily initialized per worker process and shared across sessions.
Session Close releases only that room's resources; native globals stay alive.
On normal CLI return, after CLI/server teardown, the starter disposes the runtime
with a five-second wait bound. Error paths in the existing CLI that call
`os.Exit` bypass Go defers; process termination reclaims native resources there,
but graceful native disposal is not asserted for those paths. A hung native call
or crash still affects the worker process; the helper remains the isolation option.

Diagnostics distinguish `mode: inprocess`. `reaped: false` and
`runtime_disposed: false` at room teardown are expected: there is no child process
and other sessions may still use the runtime. Successful global shutdown logs
`experimental native in-process runtime stopped` with `disposed: true`.
The shared integration harness retains the historical `helper_cleanup` JSON key;
read its `mode` field when reviewing in-process results.

## Local evidence

Host: macOS 27.2.1 ARM64, Go 1.27.4, LiveKit server 1.13.11.
Native FFI 0.12.80, rust-sdks
`5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, dylib SHA256
`41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735`.

Three sequential ten-second sessions ran in **one race-instrumented Go process**
using actual starter session wiring, native input/output and local publishers.
They used no paid STT/LLM/TTS. The native Rust/C++ binary is not race-instrumented.

| Scenario | PCM / wall seconds | Maximum input gap | Output marker frames (990 / 1320 Hz) |
|---|---|---|---|
| Session close | 10.000 / 10.000350 | 27.143 ms | 51 / 51 |
| False-interruption recovery | 10.000 / 10.000160 | 29.160 ms | 51 / 54 |
| Publisher disconnect | 10.000 / 10.000280 | 22.835 ms | 51 / 51 |

All rooms had two participants; the prepared Pion room was not connected.
Selected native handles were released at each session end. The shared native
runtime remained alive across sessions and its final Close returned successfully.
False-interruption recovery resumed the same speech through the real agent
hooks after the test's configured two-second timeout (observed 2,001.079 ms).
VAD start/end events were injected without a transcript; this was not an acoustic
VAD or cloud echo test. Existing PCM/content gates were retained. Helper cleanup
gates are unchanged for helper runs; the in-process branch asserts room release
without claiming child reaping or per-room global disposal.

Passed checks:

- Existing helper/starter tests with `-race`, default build.
- Tagged configuration and playback/accounting/false-recovery regressions.
- Nested wrapper tests with `-race`, including selected-track subscription readiness.
- Three-session starter native integration with `-race` (39.296 s package time).
- Tagged `go vet`, architecture check, default and tagged starter builds.
- Default dependency graph excludes `native-receive-probe/nativeinprocess`.

Raw results are in `examples/native-receive-probe/results/starter-inprocess-20261116/`;
[summary and source hashes](NATIVE_INPROCESS_RESULTS.json) retain exact evidence.
Earlier result files and reports were preserved. No full-repository test pass is
claimed; unrelated earlier failures remain separate.

Reproduce against the local test server:

```sh
# Terminal 1, repository root:
livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml

# Terminal 2; use a NEW output directory:
GOCACHE="$PWD/examples/native-receive-probe/.cache/go-build" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/starter-inprocess-new" \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessNative$' -count=1 -timeout 120s -v
```

## Rollback and limits

Restart with `RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=0` and
`RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=1` plus the existing helper/library paths to
use the primary helper connection. Set all three flags to `0` to use default
Pion. Drain active sessions before restarting; there is no live mode switch.

This path is ready for an **opt-in cloud test**, not default promotion. Remaining
work includes the two-minute cloud run, a longer/concurrent agent soak, clean-host
packaging, fatal-exit shutdown coverage and the documented room-I/O compatibility
gaps. Reconnect was previously tested at wrapper level; the accepted native
reconnect gap still applies. This starter run did not inject a signaling outage.
