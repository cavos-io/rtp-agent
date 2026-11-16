# Experimental single native room for agent-starter-go

Opt-in audio path: caller plus **one native agent participant**. Worker registration
stays in Go; the assigned primary room token goes to a Go helper process that
owns native input and output. The prepared Pion room never joins. No secondary
subscriber or Python process is used. Both flags unset/0 retain the existing
transport. The older receive-only experiment remains separate and unchanged.

## Build and test

Use the existing probe setup in `../native-receive-probe/README.probe.md` for the
pinned native library and generated schema. Rebuild the helper: duplex protocol
v2 supports partial output frames without adding silence. A v1 binary is rejected.
From the repository root:

```sh
go -C examples/native-receive-probe build -race -o .cache/probe-primary .
go build -race -o examples/native-receive-probe/.cache/agent-starter-go ./examples/starter

export LIVEKIT_AGENT_NAME=agent-starter-go
export RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0
export RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=1
export RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary"
export LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib"

examples/native-receive-probe/.cache/agent-starter-go start
```

Keep the usual LiveKit/provider/Silero configuration. In another terminal using
**the same LiveKit project**:

```sh
lk perf agent-load-test --rooms 1 --agent-name "agent-starter-go" --duration 2m
```

`examples/local` is also wired, preserving its existing application settings and
DeleteRoomOnClose behavior. Build the package, not `main.go` alone:

```sh
go build -race -o examples/native-receive-probe/.cache/agent-local ./examples/local
examples/native-receive-probe/.cache/agent-local start
```

Look for `experimental native primary room enabled`, normal
`experimental native primary source ended`, and `experimental native primary
stopped` with PCM seconds. A real helper failure ends the job; there is no silent
fallback. To restore default transport restart with **both** experimental flags 0.
Setting both flags to 1 is rejected.

## Ownership, playback and limits

- `StartSessionOptions.Transport` is the opt-in worker initialization seam. Nil
  follows the existing room setup. An explicit transport starts under session
  tracing and participates in recording/report/shutdown. It owns media;
  subsequent Pion joins on that job are rejected. Session.Room is nil.
- Caller discovery uses RoomService.ListParticipants (HTTP, no participant), then
  matches the native participant identity and audio track SID exactly. The helper
  receives the worker-assigned primary room token via stdin with a restricted
  environment. API key/secret must permit participant listing. Set
  `RTP_AGENT_NATIVE_PARTICIPANT` for explicit caller selection. Ambiguous audio
  publications fail startup. Startup waits for input subscription AND native
  output publication, bounded to 30s.
- Input remains native WebRTC 24kHz mono, 50ms PCM. Eight frames of cancellable IPC
  backpressure and a two-second application-consumer watchdog apply. No custom
  receive pacing, jitter, loss, DTX or concealment logic is added.
- Output accepts PCM16 mono/stereo, reuses the core streaming resampler, downmixes
  stereo, and submits up to 50ms to native CaptureAudioFrame. Flush sends the exact
  resampler tail. Clear waits for the in-flight capture then calls native
  ClearAudioBuffer; pending old-generation submissions are rejected. Native owns
  the source queue (200ms) and encoding.
- WaitForPlayout accounts for submitted samples and elapsed local time, following
  the inspected Python RTC 1.1.21 AudioSource queue contract. **Capture completion
  is not playout completion.** Playback position/drain are source-queue estimates,
  not remote acoustic acknowledgement. Playback-start timing uses first capture
  submission. This is Go regression evidence plus reference source inspection;
  no bit-identical or cross-runtime output parity is claimed.
- Normal caller departure and track unsubscribe end the session. Unexpected EOF,
  malformed IPC, capture failures, consumer stalls and nonzero helper exits stay
  errors. Shutdown signals the helper, waits, kills after a three-second bound
  when necessary, and reaps it. Recording uses the existing Go RecorderIO.
- Audio-only experimental scope: LiveKit transcription/text/data streams, RPC,
  participant state attributes, avatars/background audio, pause/resume soft
  interruption, input processors, seamless track replacement and reconnection
  are not provided by this adapter. Core STT/LLM/TTS and hard interruption remain
  in Go. Cloud dispatch and paid-provider conversation still need the user's load
  test; provider-free local tests cannot prove those paths.

## Validation

Provider-free integration uses the actual starter entrypoint function,
ExecuteLocalJob's assigned primary token, AgentSession input, PipelineAgent output
callback, playback controller, recording and job shutdown. Only the test caller
uses Pion. Tests run with Go race instrumentation in parent and helper; native
library internals are not race-instrumented.

With the local server from `NATIVE_RECEIVE.md` running:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/primary-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestStarterNativePrimaryLocal$' -count=1 -timeout 2m -v

go test -race ./examples/internal/nativeroom ./examples/starter
go test -race ./interface/worker -run 'Test(StartSessionExplicit|JobContextStartSession)'
go -C examples/native-receive-probe test -race -run TestDuplex
```

The result directory must be new. Tests preserve logs, per-session reports,
recordings and measurement summaries. Fixed gates: exactly two participants;
5s continuous-packet tone/silence/tone input within250ms of wall; callback gap
<=250ms; both output tones detected; 1s drain position; clear interrupts a queued
200ms burst; cleanup<=5s. This is not a DTX/loss or long-duration stability test.

Native packaging remains FFI0.12.80 / rust-sdks
`5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, Python inspection1.1.21,
server-sdk-go2.17.0, local server1.13.11. Tested on macOS27.2.1 ARM64 with Go1.27.4.
The worker adds no CGO dependency; helper requires CGO, dlopen, the pinned library,
and existing probe/libopus dependencies. Linux artifacts require matching loader
setup and remain unverified here; Windows is unsupported by this helper.
Distribute native/WebRTC and third-party license notices with the binary. No
system dependencies were installed and no default transport code was replaced.

## Measured integration results

Results: `../native-receive-probe/results/primary-starter-20261115/`.
All 2,972 pre-existing artifact hashes verified unchanged. Pilot runs remain
alongside the final results; no prior failures or acceptance gates were edited.

| Final case | Participants during / after | Input PCM / wall seconds | Max input gap | Cleanup |
|---|---:|---:|---:|---:|
| Session close | 2 / 1 | 5.000 / 5.0002 | 55.9ms | 3.063s |
| Publisher disconnect | 2 / 0 | 5.000 / 5.0000 | 52.5ms | 3.013s |
| Track unsubscribe | 2 / 1 | 5.000 / 5.0006 | 52.1ms | 3.234s |
| Two-minute session | 2 / 1 | 120.000 / 120.0006 | 67.4ms | 3.042s |

All passed. The two-minute run passed all 120 one-second continuity windows
(maximum PCM/wall difference3.6ms). Both output tone markers were detected in
51 decoded packets each. Recording files and report recording paths were
populated. No Go race report appeared in these tests. Cleanup includes session
finalization, not just native disposal. These measurements do not establish
remote acoustic latency or multi-session resource stability for the new path.

To repeat the longer local check, add `NATIVE_ROOM_DURATION=2m`, select
`-run '^TestStarterNativePrimaryLocal/session-close$'`, use a new result directory,
and increase `-timeout` to `3m` in the integration command above.

Repository gate limitation: the focused parity case passes, but the existing
`parity-gate.sh` wrapper fails because it calls `check-test-integrity.sh` without
its required arguments. Explicit integrity/analyzer checks for the changed files,
vet, architecture checks, and default/receive-only live regressions pass. The
full manifest attempt was stopped after reproducing this independent wrapper
failure; no full parity-suite pass is claimed. See the preserved `VALIDATION.md`
and `focused-wrapper.log` in the result directory. Unrelated gate scripts remain
unchanged.

The subsequent dispatched cloud run and dashboard confirmation of exactly two
participants are checkpointed in [CLOUD_NATIVE_PRIMARY.md](CLOUD_NATIVE_PRIMARY.md),
including the original screenshot and input artifact hashes.

## Periodic diagnostics

The primary-native adapter logs `experimental native primary PCM continuity`
every10s, plus a final partial window. Fields include wall duration, callback PCM
duration, cumulative PCM, maximum inter-frame callback gap, time since the last
frame, and first-frame delay from helper launch. Timing uses the parent monotonic
clock. The first window includes startup; the final window includes shutdown.
These are observations at the helper-to-agent callback, before mute filtering;
they are not RTP loss/DTX measurements or downstream retention guarantees.

`experimental native primary helper cleanup` reports PID, actual wait/reap status,
exit code, kill attempts, native disconnect callback, retained handle release,
global runtime disposal and callback overflow. `cleanup_ms` measures the parent's
teardown/wait phase; `native_cleanup_ms` is the helper's own native teardown
interval. They are durations from separate clocks, not timestamps to subtract.
The source-departure path can finish native teardown before parent cleanup starts.
No receive admission, scheduling, resampling or playback policy changes are made
by these diagnostics.

Five sequential primary sessions (one race-instrumented Go process; newly built
race helper) can be run against the local server using:

```sh
go -C examples/native-receive-probe build -race -o .cache/probe-primary-sequential .
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/primary-sequential-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary-sequential" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestNativePrimarySequentialSessions$' -count=1 -timeout 14m -v
```

The new output directory contains per-session recording/report/measurement files
and a resource census after each3s settle and GC. Sessions exercise close,
publisher disconnect, unsubscribe, close, unsubscribe in order. Each uses the
existing2min PCM, two-participant, output-marker, drain and clear gates, and
explicit native-cleanup flags. Resource limits are warm cycle1+5 goroutines and
+16MiB live Go heap; RSS is reported without a fixed gate. Native memory is not
covered by the Go race detector. No cloud or paid-provider calls are needed.

Measured five-session results: [PRIMARY_SEQUENTIAL_SESSIONS.md](PRIMARY_SEQUENTIAL_SESSIONS.md).
