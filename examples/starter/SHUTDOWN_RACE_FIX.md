# Session shutdown race fix and rerun

The usage reporter was canceled but not joined. A tick already selected by its
loop could still append to ChatCtx while final telemetry iterated that history.
AgentSession now owns a per-run completion channel and waits for the reporter
outside its mutex before flushOTelTurnMetrics. Start/Stop lifecycle serialization
keeps completion ownership specific to the run. No transport, STT, TTS or playout
policy changed.

Regression tests first failed without the join. Focused reporter, restart,
telemetry-flush and concurrent Start/Stop tests then passed under the race
detector for ten repetitions. The restart test uses a controlled assistant to
isolate usage-reporter lifetime from provider background workers.

## Five-session rerun

Same frozen gates and publisher as the preserved failed run. One Go process,
PID15835, five sequential120s local native sessions. All five passed under the
race detector, with no race reports. Each unpublish completed shutdown within5s.

| Cycle | PCM / wall seconds | Max callback gap ms | Goroutines after settle | Live heap MiB | RSS MiB | Remaining helpers |
|---|---|---|---|---|---|---|
| 1 | 120.000 / 120.000 | 58.009 | 2 | 4.653 | 161.391 | 0 |
| 2 | 120.000 / 120.001 | 57.238 | 2 | 4.688 | 145.469 | 0 |
| 3 | 120.000 / 120.001 | 53.543 | 2 | 4.721 | 173.781 | 0 |
| 4 | 120.000 / 120.000 | 59.804 | 2 | 4.733 | 156.453 | 0 |
| 5 | 120.000 / 120.001 | 128.973 | 2 | 4.784 | 141.953 | 0 |

All600 one-second windows passed; maximum rounded-log duration difference1ms.
Tone energy was present throughout the five sessions in aggregate. Callback gap
maximum128.973ms passed the unchanged250ms gate. Duration matching is not proof
of marker ordering, verified DTX behavior or end-to-end latency.

Settled goroutines stayed at2. Live heap increased about134KiB across five cycles;
RSS fluctuated and ended below cycle1. This does not show accumulating helpers
or large live-Go-heap growth, but does not prove long-term native leak freedom.
All2915 previously captured artifact hashes verified unchanged, including the
original failed five-session log. The server was stopped and no helper remained.

## Validation limits

- Core package vet and diff checks passed.
- Full non-race core/agent suite remains failing at
  TestAgentSessionOffUsesCallbackIdentity: first listener did not receive event.
  Reproduced against HEAD source using a Go build overlay, without this fix.
  See baseline-listener-failure.log. This unrelated failure was not suppressed.
- An initial broader race selection also exposed MultimodalAgent Start/run and
  realtime-close test races. The usage-reporter restart regression was narrowed
  to a controlled assistant; no multimodal fix or general race-free claim is made.
- Initial sandbox-only run could not bind httptest sockets; localhost-enabled
  retry reached the unrelated listener failure. Both logs are retained.
- Full-suite integration readiness is therefore not claimed despite the targeted
  fix and five-session rerun passing. No TTS error investigation was performed.

## Evidence and reproduction

New artifacts: ../native-receive-probe/results/sequential-sessions-shutdown-fix/.
Original failed artifacts remain in sequential-sessions/. Reproduction command
and local server setup are unchanged from SEQUENTIAL_SESSIONS.md.

```sh
go test -race ./core/agent -run '^(TestStopWaitsForUsageReporter|TestUsageReporterJoinedAcrossSessionRestarts|TestAgentSessionStopFlushesOTelTurnMetrics|TestStopDuringStartHonorsContext|TestStopDuringStartWaitsThenTearsDown)$' -count=10
```
