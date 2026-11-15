# Native receive shutdown checkpoint

User-run cloud load test: room `RM_shs7mKtSH6UA`, job `AJ_BNiWkPNLndwL`.

- Native receive first PCM at 20:21:03.660 +0700 on 2026-11-15.
- Source ended at 20:22:58.791 with INFO classification `track_unsubscribed`.
- Session close: `participant_disconnected`, no close error; job shutdown reason: `native receive source ended`.
- 2301 frames / completed callbacks, 115.050s PCM versus 115.131s first-frame-to-end-log wall time.
- Zero queue waits; longest completed callback 0.398ms.
- 56 periodic STT reports track cumulative wall time within approximately 52ms; total reported STT audio 114.300s.
- No native helper failure reported. Final TTS error is excluded from this checkpoint investigation at user request.

Evidence is aggregate duration and lifecycle verification, not per-frame latency or loss-free content proof. Repeated sessions in one worker process remain a separate pending validation.

Regression coverage includes normal publisher/track departure, malformed end events, unexpected EOF, nonzero exit after an end event, native cleanup failure, bursts and stalled consumers. Default transport remains unchanged.

## Source artifact SHA-256

- `observability-RM_shs7mKtSH6UA.zip`: `839660195b819508b8b746edf936fac80aa66b80dc2dc30c98f519e5e46fed30`
- `session_report.json`: `d7e05ce0660e5d026511c2b12981a1a511fe4125f3237541a910081bb5200742`
- `session.log`: `12e006ca13a380de7fa89726cbca985a234f9dc24fb303a315b57e5a7f9967e9`
