# Five sequential native receive sessions

Completed five 120-second sessions in one race-instrumented Go test process
(PID 14466). Each session created a real native helper and local LiveKit room,
received a continuous 1kHz Opus tone through the actual agent-input hook, then
unpublished the selected track. Providers were bypassed. This exercises repeated
session ownership in one process, not cloud worker-dispatch reconnection.

**Overall test FAILED:** cycle 1 exposed two race-detector reports for the same
core shutdown interaction. `AgentSession.flushOTelTurnMetrics` reads
`ChatCtx.Items` (agent_session.go:3808) while `reportUsageLoop` calls
`ChatContext.Append` (agent_session.go:2742, llm.go:356). Cycles 2–5 passed.
No production fix or suppression was made; the failed result is retained.

## Measurements

| Cycle | PCM / wall seconds | Max callback gap ms | Settled goroutines | Live Go heap MiB | RSS MiB | Helper children |
|---|---|---|---|---|---|---|
| 1 | 120.000 / 120.000 | 86.947 | 2 | 4.605 | 159.609 | 0 |
| 2 | 120.000 / 120.001 | 81.370 | 2 | 4.696 | 126.406 | 0 |
| 3 | 120.000 / 120.001 | 63.383 | 2 | 4.718 | 163.016 | 0 |
| 4 | 120.000 / 120.000 | 235.073 | 2 | 4.796 | 149.016 | 0 |
| 5 | 119.950 / 120.000 | 71.808 | 2 | 4.741 | 175.297 | 0 |

All five PCM-duration checks passed the existing 500ms gate. All 600 one-second
windows passed the supplemental 250ms duration gate (largest difference176ms,
from rounded log values); maximum callback gap235.073ms passed the frozen250ms
gate. Tone energy was present each cycle. This is cadence/duration and basic
content evidence, not marker order, silence/DTX, or end-to-end latency proof.

Each selected-track unpublish completed job shutdown within the existing5s
bound. Helpers were absent after every cycle and after the run. Settled
measurements used3s plus GC. Live Go heap grew about139KiB from cycle1 to5;
goroutines stayed at2. RSS varied126.4–175.3MiB, ending15.7MiB above cycle1.
That does not establish a monotonic leak, but five samples cannot prove long-term
native/cgo leak freedom. RSS includes race-runtime overhead and retained memory.
The +16MiB live-heap and +5 goroutine supplemental gates passed unchanged.

Raw evidence: `../native-receive-probe/results/sequential-sessions/` contains the
pre-run contract, preservation hashes, smoke logs, five-session log and summary.
All2907 prior artifact hashes verified unchanged. Initial sandbox socket denial
is retained separately. Test-owned localhost server was stopped. No helpers remain.

## Reproduce

Start the isolated server documented in NATIVE_RECEIVE.md, then from repo root:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-helper" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativereceiveintegration \
  -run '^TestNativeSequentialSessions$' -timeout 14m -count=1 -v ./examples/starter
```

The test requires macOS/Linux `ps` and `pgrep`. Same macOS ARM64 environment and
native pins as NATIVE_RECEIVE.md. Tagged vet and diff checks passed. The next
blocking investigation is the core session shutdown race; transport/playout
behavior and TTS remain outside this verification change.
