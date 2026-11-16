# Cloud shutdown validation — RM_BTR6jH4cHKhG

2026-11-17. User-provided two-minute echo run using examples/local with in-process native enabled and the latest shutdown fix. Repository checkpoint at review: `4818e47fc`. The log confirms native mode, but does not independently attest the binary's build revision. No production transport changes or new cloud run were made during this evidence checkpoint.

## Result

PCM continuity and shutdown validation passed:

| Measure | Observed |
|---|---:|
| Steady PCM / wall | 100.000 / 100.000511 seconds |
| Total PCM | 117.22 seconds |
| Total observed interval | 118.464436 seconds |
| First-frame delay | 1165.080 ms |
| Maximum PCM callback gap | 42.482 ms |
| Log warnings / errors | 0 / 0 |
| Report error events | 0 |
| Native session cleanup | 67.493 ms; handles released; disconnect callback received |
| Worker runtime disposal | disposed=true |
| False-interruption recovery | one resumed=true event |

The initial/final partial intervals include startup and final idle time. The ten steady windows show no accumulated PCM deficit. This is application callback evidence, not packet-loss, DTX, content identity, or end-to-end latency proof.

Normal track departure logged `track unsubscribed` and `audio output ended normally`. There was no TTS failure on departure, already-stopped session warning, queue overflow, or closed-pipe warning. Both `session_close` and `drain_agent_activity` completed without error status. The session finished at 06:48:13 +0700; global runtime disposal occurred when the worker stopped at 06:49:38 +0700. Per-room runtime_disposed=false is expected for the shared runtime.

## Separate follow-up: zero-audio TTS

At approximately 06:47:43 +0700, before shutdown, one TTS request and its nested run produced two error spans: `tts_request` and `tts_request_run`, reporting `no audio frames were pushed`. Associated metrics showed audio_duration=0 and ttfb=-1. No corresponding report error event or warning was emitted. These are nested spans for a request, not evidence of two independent failures.

Cause is unresolved. Do not attribute this to the native transport, provider service, or interruption cancellation without investigation. Follow-up should correlate the request with speech cancellation/interruption, provider stream completion, and emitted audio frames; then distinguish expected cancellation from a genuine empty response and add regression coverage at the responsible boundary. The full original message remains in the preserved archive; transcript text is omitted from this checkpoint.

This separate issue does not invalidate the shutdown fix, but this run must not be described as having no trace errors anywhere.

## Preservation and limits

`manifest.json` records hashes and local locations of the archived report, log and observability zip. Raw inputs remain under `examples/native-receive-probe/results/cloud-RM_BTR6jH4cHKhG/`; raw cloud data is not committed. `analysis.json` records measurements; `verification.json` records shutdown span status and follow-up span identifiers.

Verified 3441 earlier result files unchanged, plus archived cloud inputs for RM_Bey97Q99oiWs and RM_9gJhyMGhpXrc. Failed runs remain preserved. No fixes, defaults, helper behavior, acceptance gates or earlier result files changed. No new live test was needed for this evidence-only checkpoint. This confirms the observed cloud shutdown scenario, not readiness to promote all native room features to default.
