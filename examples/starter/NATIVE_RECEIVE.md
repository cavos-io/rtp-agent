# Experimental native audio receive for agent-starter-go

For the separate opt-in **two-participant** input/output path, see [NATIVE_ROOM.md](NATIVE_ROOM.md).

Opt-in only. The starter keeps its Pion worker/room connection and audio output,
but receives caller PCM from the pinned LiveKit native engine in a Go helper
process. The helper joins as a hidden, receive-only agent participant. This adds
one process and one room connection per job; it is not an in-process Go binding.
No Python process is used at runtime.

## Build and run

From the repository root, using the existing LiveKit project and provider
configuration (`LIVEKIT_URL`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`, and the
starter's usual model/voice and Silero runtime settings):

```sh
# Existing probe setup supplies the pinned FFI library and generated schema.
# On a fresh checkout, first follow ../native-receive-probe/README.probe.md.
go -C examples/native-receive-probe build -o .cache/probe-helper .
go build -o examples/native-receive-probe/.cache/agent-starter-go ./examples/starter

export LIVEKIT_AGENT_NAME=agent-starter-go
export RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=1
export RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-helper"
export LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib"

examples/native-receive-probe/.cache/agent-starter-go start
```

In another terminal configured for **the same LiveKit project**:

```sh
lk perf agent-load-test --rooms 1 --agent-name "agent-starter-go" --duration 2m
```

The CLI reads exported LiveKit environment variables or its selected project;
the worker's dotenv configuration does not automatically configure that other
terminal. This command exercises the configured STT, LLM and TTS providers.
Those calls were not made during local validation.

Look for `experimental native receive enabled`, then
`experimental native receive first PCM`. Shutdown logs
`experimental native receive stopped` with `pcm_seconds` (frames received at the
integration boundary, not proof that downstream consumers retained every frame).
A failed helper ends the job with an explicit error; it never falls back silently
to Pion audio input.

For the default transport, restart with:

```sh
RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0 \
  examples/native-receive-probe/.cache/agent-starter-go start
```

Unset/0 invokes the original `StartSession` call and does not start a helper or
require the native library. The default build has no new FFI link dependency.
The helper requires CGO and the existing probe dependencies, including libopus
for the probe publisher code. Only macOS ARM64 was tested. Linux needs a matching
native `.so` and its normal loader requirements; Windows is not supported by
this helper's current dlopen/signal implementation. No dependencies are installed
automatically.

## Selection and lifecycle

- Intended first test: one caller per room, as in the command above. By default,
  select the first eligible caller and its first audio publication. Set
  `RTP_AGENT_NATIVE_PARTICIPANT=echo-participant` to explicitly select lk perf's
  publisher (lk 2.19.4), or supply another caller identity for manual rooms.
- The helper matches both participant identity and exact audio track SID. Agent
  output and other tracks cannot reach its PCM stream. The native room currently
  auto-subscribes, so other publications may still use network/native resources;
  only the selected track is exposed as PCM.
- Pion auto-subscription and RoomIO input are disabled only for opted-in jobs.
  Its output and room lifecycle remain active. PCM enters the existing Go session
  at 24kHz mono, 50ms frames. Existing session input recording is tapped, and
  explicit session input muting is respected.
- This starter-only bridge bypasses RoomIO's input processor and input-track
  tracing/transcription-track assignment. The starter configures no input
  processor. Do not treat RoomIO input-track telemetry as native-receive timing.
  STT adapters, interruption logic and downstream queue behavior are unchanged.
- Startup has bounded waits. The PCM pipe uses versioned JSON/base64 framing,
  validates frame size and maintains an eight-frame consumer queue. A full queue
  now waits for the consumer or cancellation instead of treating a burst as an
  overflow error. Frames are neither dropped nor reordered. A consumer callback
  that takes more than two seconds cancels the experimental path with an explicit
  stalled-consumer error; this is an application liveness bound. There is no
  custom jitter, silence, clock, packet-admission or time-stretch policy.
- Tokens are short-lived, scoped to the room, hidden and receive-only. They are
  passed through stdin, not command-line arguments or result files. The helper
  receives a restricted environment, not the worker's provider credentials.
- Session/job shutdown cancels the helper, sends interrupt, and waits for exit;
  after three seconds it kills/reaps a stuck helper. A selected track unsubscribe
  or caller disconnect ends this experimental path. Seamless track replacement,
  room migration, multiple selected callers and long-lived token renewal are
  outside this experiment.

The helper reuses native FFI0.12.80 / Rust SDK
`5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`. Use the binary/schema/library from the
same pinned setup, and retain its license/third-party notices when distributing.
The native reference stream retains its existing queue settings; this experiment
does not establish bounded native heap usage under every host stall.

## Verification

Offline checks:

```sh
go test -race ./examples/starter ./examples/starter/internal/nativereceive
go -C examples/native-receive-probe test -race ./...
go vet ./examples/starter ./examples/starter/internal/nativereceive
```

Provider-free live check uses the actual starter integration, native helper and
an Opus tone publisher, with an input hook observing real PCM before providers.
It checks the default path, native path, disabled Pion subscriptions, normal
shutdown and shutdown after the selected track is unpublished.

Start an isolated local server (ports must be free):

```sh
cat >/tmp/rtp-native-test.yaml <<'YAML'
port: 18991
bind_addresses: [127.0.0.1]
rtc:
  use_external_ip: false
  tcp_port: 18992
  port_range_start: 19000
  port_range_end: 19100
keys:
  devkey: local-native-receive-experiment-secret
YAML
livekit-server --config /tmp/rtp-native-test.yaml
```

In another terminal, with the helper/library exports above:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
  go test -race -tags nativereceiveintegration \
  -run TestStarterReceiveLocal -count=1 -v ./examples/starter
```

Local validation passed on macOS27.0.1 ARM64, Go1.27.4, LiveKit server1.13.11:
three-second tone input reached the session as 2.85s PCM through default Pion,
2.95s through native, and 3.20s in the native track-loss trial (native playout
includes startup/tail frames). Tone energy was present in every case. These
short smoke measurements are not latency/continuity acceptance results.
All three race-instrumented tests and bounded job shutdown checks passed.

Installed lk2.19.4 help and pinned upstream source were checked: it publishes
`echo-participant`'s audio track before dispatch and echoes the agent's output
after its configured delay. Its startup order is compatible with this bridge.
The paid two-minute load test itself was not run. Existing controlled recordings,
failed results and acceptance gates remain untouched; this is not a claim that
the original latency incident is fixed.


## Queue-overflow follow-up

The cloud run on November15 exposed an IPC defect: the nonblocking enqueue
rejected a buffered burst whenever the eight-frame channel was momentarily full.
That implementation is replaced with cancellation-aware backpressure, without
increasing channel capacity or changing native playout.

Shutdown/failure logs now include `ipc.received_frames`, `completed_callbacks`,
`queue_waits`, `max_queue_wait_ms`, and `max_consume_ms`. Completed callbacks do
not prove downstream STT retained every frame. Consumer functions must honor
cancellation; the watchdog cannot forcibly unwind arbitrary Go code. Backpressure
does not prove bounded latency or native memory under sustained downstream
under-capacity. No PCM is padded, stretched, reordered or selectively discarded.

Rebuild and restart the agent to pick up this fix. The native helper protocol and
binary are unchanged. For the tracked starter:

```sh
go build -o examples/native-receive-probe/.cache/agent-starter-go ./examples/starter
```

The same IPC fix was applied to the existing ignored `examples/local` copy in
this workspace, preserving its `DeleteRoomOnClose` setting. If that is your
entrypoint, rebuild it or use `go run ./examples/local start` with your existing
native-receive environment. An already-running agent still uses the old code.

A longer provider-free validation can be selected using:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 NATIVE_TEST_DURATION=2m \
  go test -race -tags nativereceiveintegration \
  -run 'TestStarterReceiveLocal/native$' -count=1 -v ./examples/starter
```

See [queue-fix evidence](../starter/QUEUE_FIX.md) for results and readiness limits.

## Publisher departure classification

Selected publisher disconnect and selected-track unsubscribe now produce an
explicit `ended` IPC event after native cleanup. The agent logs
`experimental native receive source ended` at INFO and ends the job normally.
Unexpected EOF, invalid terminal events, nonzero helper exit, native errors,
and consumer stalls remain failures. The default transport is unchanged.

Rebuild **both helper and agent** for this change (an older helper still reports
track loss as failure). For the existing local example and environment:

```sh
go -C examples/native-receive-probe build -o .cache/probe-helper .
go build -o examples/native-receive-probe/.cache/agent-local ./examples/local
examples/native-receive-probe/.cache/agent-local start
```

Regression tests cover both departure reasons, unexpected EOF, a nonzero exit
following a valid terminal event, invalid terminal sequences, and existing
burst/stalled-consumer behavior. These tests do not replace the next cloud run.
