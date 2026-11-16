# Native shutdown ordering and controlled recovery — 2026-11-16

Example-only, opt-in primary native adapter. Default transport, native receive
policies, helper protocol and provider adapters are unchanged.

## Cloud evidence retained

Room RM_EKhy8ccCZCFr exercised17 provisional pauses (12 speaking,5 thinking),
12 interrupted assistant messages and no reported false-interruption recovery.
Steady input advanced100s PCM in100.000243s wall time. Job termination then logged
`signal: interrupt` despite completed native cleanup, followed by a closed-pipe
session-stop warning. The later canceled Deepgram reconnect is separate.

Original ZIP, report and log are copied intact under
`examples/native-receive-probe/results/primary-shutdown-recovery-20261116/cloud-RM_EKhy8ccCZCFr/`.
Their SHA256 values are:

| File | SHA256 |
|---|---|
| observability-RM_EKhy8ccCZCFr.zip | 0b620713fc62acde9ede8c2c2ff1c9fe5383f64e665d6445e77172ef90bc95e0 |
| session_report.json | fe808f8eccd1e8dd5690be4e658f19880f1f8b1aa4e2bb34916a44521ef70862 |
| session.log | c96b5683915b3e5f768689ae4d5edf7ee9e436a5d6eb6c8baec7072cdd0ab75e |

A separate manifest verified all3113 pre-existing result files unchanged. The
initial failing regression is preserved as `baseline.log` in this new run root.

## Fix and regression contract

Previously, cancellation sent SIGINT even if the native helper was completing
teardown. It could terminate a process whose cleanup report already indicated
success. Cancellation could also stop PCM/ACK reading before cleanup diagnostics
arrived. The cloud logs establish this ordering hazard, but not the exact
instruction at which the helper received its signal.

Shutdown now sends the existing protocol `stop` after configuration and the
current command write, then drains stdout through the final cleanup report and
waits for process exit. Canceled media delivery no longer abandons shutdown
reads. Normal teardown sends no signal. The existing three-second kill bound
remains for stuck helpers/writers. Nonzero exits, malformed output, missing
shutdown EOF and consumer stalls retain their failure classification.

The new fake-helper regression traps SIGINT and exits9; it failed before the fix.
After the fix, stop exits0 with cleanup retained, while an intentional exit7 stays
an error. Another regression holds a capture ACK, verifies capture1000 precedes
stop1001, releases the canceled command and rejects subsequent clear with
context cancellation. Existing forced-kill and stalled-consumer tests still pass.
These IPC tests do not claim native receive proof.

## Controlled live evidence

The recovery test injects VAD onset/end with no transcript into a provider-free
AgentActivity using the running starter's native playback/output controller.
It sets the agent state to speaking, queues real output PCM, verifies pause,
waits for the configured two-second false-interruption timer and requires
`Resumed=true` with the same non-interrupted SpeechHandle. Pending PCM then
reaches the actual Opus-decoding caller. No STT, LLM or TTS is invoked.

This tests core recovery plus native playback. It does not test whether acoustic
noise triggers production VAD or whether a provider recognizes unwanted text.
The controller session is isolated from the starter's provider pipeline; no
claim of a completed cloud conversational false-interruption test is made.

Predeclared gates: recovery1.9–3.0s after injected end; two participants; no output
marker growth during200ms after300ms settling; at least20 decoded frames of each
marker; total and per-second PCM/wall discrepancy <=250ms, callback gap <=250ms;
helper reaped, exit0, no kill, all cleanup flags true and no callback overflow.
Existing drain, hard-clear and lifecycle gates were retained.

| Scenario | PCM / wall seconds | Max callback gap | Helper exit |
|---|---:|---:|---:|
| Controlled false recovery | 10.000 / 10.000633 | 52.03ms | 0 |
| Job termination while publication paused | 5.000 / 5.000512 | 52.82ms | 0 |
| Session close | 5.000 / 5.000050 | 52.71ms | 0 |
| Publisher disconnect | 5.000 / 5.000284 | 52.68ms | 0 |
| Track unsubscribe | 5.000 / 5.000182 | 53.51ms | 0 |

Recovery occurred at2000.793ms on the shared parent monotonic clock. The receiver
detected52 pre-pause marker frames and53 resumed marker frames. All five scenarios
passed under the Go race detector with a race-built helper, without forced kills,
cleanup failures or closed-pipe shutdown warnings. Native library internals are
not instrumented by Go's race detector. Exact acoustic stop latency is unmeasured.

## Verification and reproduction

- Full nativeroom/starter race suites passed. Client lifecycle selection passed3
  repeats; new shutdown/recovery selection passed10 repeats.
- Focused speaking-recovery parity case, architecture, vet, explicit-file
  test-integrity and targeted analyzers passed. No staged-only coverage claimed.
- Prior mock-counter race and parity-wrapper argument issue remain documented in
  NATIVE_PLAYBACK_RECOVERY.md. No full repository race/parity-suite pass claimed.
- Platform and artifacts unchanged: macOS27.2.1 ARM64, Go1.27.4, server1.13.11,
  FFI0.12.80/rust-sdks5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb.

Start the local server with the preserved server.yaml (port18991), then from the
repository root, selecting a fresh result directory each time:

```sh
go test -race ./examples/internal/nativeroom ./examples/starter
go -C examples/native-receive-probe build -race -o .cache/probe-primary-pause .
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/recovery-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary-pause" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestStarterNativePrimaryFalseRecovery$' -count=1 -timeout 90s -v
```

Use `TestStarterNativePrimaryJobTermination` or `TestStarterNativePrimaryLocal`
with a different output directory to repeat shutdown scenarios. Rebuild the
agent for the next cloud echo run; the existing helper protocol is unchanged.
This work does not establish that the separate provider shutdown warning is
fixed under cloud load. Keep native transport opt-in.
