# Local native lifecycle validation — 2026-11-18

Revision tested: `11b0cc229` (native implementation from `f893bd639`).
Host: macOS arm64, pinned LiveKit FFI 0.12.80, local LiveKit server 1.13.11.
Server used loopback ports 17880/17881 and UDP 17900–17950 with synthetic fixture
credentials. No STG or external provider calls. The temporary server was stopped.
Pion remains default; downstream files were neither edited nor committed.

## Results

| Check | Result |
| --- | --- |
| Two native rooms: stalled consumer isolated from healthy room | Passed; stalled queue bounded at 25 frames; healthy room delivered 2s PCM in 2.00017s, maximum observed gap 24.711ms |
| Invalid-token connection while healthy session exists | Failed as expected without terminating the healthy session |
| Three repeated live sessions, two concurrent close callers per session | Passed; output markers observed, no wrong output marker assertion failures |
| Runtime shutdown while a room exchanges live audio | Passed; measured close/consumer/publisher cleanup interval 2.409458ms; reconnect after disposal rejected |
| False interruption pause/resume | Passed; zero output marker growth while paused; original producer resumed |
| Hard interruption | Passed; old producer canceled and replacement output markers received |
| Two-second signaling outage | Reconnected; maximum PCM gap 2479.084ms, below fixture's 3000ms gate; post-reconnect 2s PCM in 2.000098s and 99 output markers |
| Focused lifecycle/hold/isolation/CLI race tests, repeated three times | Passed in native runtime, room facade and CLI packages |

See `wrapper-summary.jsonl`, `compat-summary.jsonl` and `lifecycle-race.log`.
The wrapper tests import the current public native runtime from the root checkout;
they do not test the obsolete probe implementation. Pion test publishers provide
synthetic audio markers at 600/1000Hz and observe returned audio.

## Commands executed

From `examples/native-receive-probe`, each in a separate test process with
`PROBE_INPROCESS_URL=ws://127.0.0.1:17880`, a fresh `PROBE_INPROCESS_OUT`, and
`LIVEKIT_LIB_PATH` pointing to the pinned local library:

```sh
go test -race -tags nativeinprocess -run '^TestNativeWrapperLocal$' -count=1 -timeout=90s
go test -race -tags nativeinprocess -run '^TestNativeWrapperCompatibility$' -count=1 -timeout=90s
```

From the root checkout:

```sh
go test -race -tags nativeinprocess ./interface/worker/livekit/nativeinprocess ./interface/worker/livekit/nativeroom ./interface/cli -run 'Test.*(Shutdown|Close|Drain|Isolation|Isolates|FailedConnect|Hold)' -count=3
```

Raw logs and local server configuration remain in
`/private/tmp/native-local-20261118/`; summaries above preserve the measured results.

## Limits and remaining gates

These are short real-FFI runtime tests plus deterministic lifecycle tests. They
are not a multi-hour soak or a downstream worker process receiving SIGTERM during
a provider-backed call. The real-media shutdown fixture exercises runtime close,
not worker drain, recording finalization, or active transfer/hold shutdown.
Hold acknowledgement/cleanup and CLI cancellation ordering have unit/race coverage.

Still required: full downstream active-call and active-transfer SIGTERM/drain;
concurrent conversation/chat/caption/recording isolation and resource-trend soak;
Linux live media and deployment-specific reconnect/failure acceptance. The outage
fixture demonstrates signaling recovery only, not every ICE/network failure.
