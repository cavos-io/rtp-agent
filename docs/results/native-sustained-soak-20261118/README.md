# Sustained native local soak — 2026-11-18

Result: **mixed**. Native sustained media and resource/cleanup checks passed,
but the original run failed four initial SDK caption checks during short-call
cycles. A separate 26-session repeat passed after waiting for caller media
readiness. The matched Pion run passed with a one-second startup wait. Pion remains
the default. No product code or downstream files were changed for this test;
downstream fixtures use Go overlays. The temporary server was stopped afterward.

Follow-up: [startup investigation and fix](../native-caption-startup-20261118/README.md)
confirmed captions arrived before caller participant discovery and were dropped
by the Go SDK. The corrected fixture waits for participant discovery, with no
audio prerequisite or fixed delay. Use that fixture for new validation runs;
the original sources and failed evidence below remain historical records.

## Results

| Run | Outcome | Duration / scope |
| --- | --- | --- |
| Native original | FAIL: four initial captions missing; resource/cleanup checks passed | 728.25s; two ten-minute calls, then 24 short calls |
| Native receiver-ready repeat | PASS | 133.21s; 26 short calls in one worker |
| Native race smoke | PASS; no Go race reports | 36.98s; six short calls |
| Pion comparison | PASS | 726.58s; same sustained/repeated workload, one-second wait before first text |

| Measurement | Native original | Pion comparison |
| --- | --- | --- |
| RSS during sustained phase, after first two minutes | 54.52–79.73 MiB | 35.80–69.14 MiB |
| Goroutines in that interval | 42 | 212–213 |
| RSS after short-wave cleanup, first → last | 77.94 → 69.52 MiB | 49.58 → 49.00 MiB |
| Go heap after short-wave cleanup, first → last | 8.15 → 8.27 MiB | 7.35 → 8.29 MiB |
| Goroutines after each short-wave cleanup | 13 | 2 |
| Native successful session cleanup diagnostics | 26/26 | Not applicable |

Native RSS was not monotonically increasing. The short-wave RSS regression
slope was +1.87 MiB/min despite ending below the first sample: residency varied
between 55.33 and 77.94 MiB. This is bounded observed residency, not proof against
all leaks. The receiver-ready repeat stayed within the same preselected bounds:
post-cleanup RSS 97.52–106.39 MiB, heap 7.64 → 8.71 MiB and 12 goroutines.
Different RSS residency across runs reinforces the measurement limitations below.

The two sustained native callers sent 60,029 tone frames and received 60,005
expected-tone frames, with zero alien-dominant frames. Each completed 40 unique
chat turns with matching acknowledgements, agent.events transcripts and SDK
final captions. All 26 worker sessions had zero detected alien-dominant input
frames and zero publish-failure counters. The four failed short peers exited
before their final returned-audio assertion, so the original run does not claim
complete peer-side isolation proof for those calls. All 26 receiver-ready peers
completed their audio, event and caption assertions with no crossed markers.
Native global runtime close returned successfully in all native runs, including
the original run that failed caption assertions.

## Startup caption finding

Original short-wave failures: slots 1/0, 2/0, 3/0 and 7/0 (wave/slot, zero-based).
Each received the correlated agent.events transcript, but the caller's
OnTranscriptionReceived callback did not receive the final caption within eight
seconds. These are SDK transcription-packet checks, not direct observations of
the frontend or the separate lk.transcription text-stream path.

The diagnostic repeat logged participant_known=false and zero returned frames
at ready_for_tester in all 26 sessions. It then waited for five returned tone
frames before sending text; all 26 passed. The Go caller SDK drops transcription
packets for unknown participants (server-sdk-go/v2 v2.17.0, room.go:1355–1375).
This supports receiver startup ordering as the explanation. We did not capture
the dropped packet itself, so it is not definitive proof of the exact loss point.

No product fix is claimed. Verify the real tester/frontend's immediate-start
caption handling or readiness contract before closing this finding. Waiting in
the diagnostic fixture does not turn the original failed run into a pass.

## Workload and acceptance

One worker process hosts two concurrent calls for 600 seconds, followed by 12
waves of two eight-second calls in the same process (26 sessions total). Each
caller runs in its own process. A real loopback LiveKit server carries Opus audio,
chat streams, captions and agent.events. The downstream native factory, session
startup and automated-test evidence components are exercised with synthetic
assistants, without provider calls or full app.Run dispatch/storage finalizers.

Each call sends a continuous 600 or 1000 Hz tone and expects the same dominant
tone in returned audio. A frame is rejected as crossed when the alien tone
amplitude exceeds 1000 and is more than three times the expected amplitude.
Every 15 seconds the caller sends unique room/run/message/turn text markers;
received acknowledgement, transcript and final caption must match exactly.
Every checked native interval must deliver at least 20 PCM frames/second; each
peer must receive at least 100 expected tone frames with zero alien-dominant
frames. Native input is 20 ms; Pion input is 50 ms and its comparison threshold
is 10 frames/second. These thresholds detect stalls, not every lost frame.

Before the sustained runs, the fixture enforced post-cleanup bounds of +32 MiB
RSS, +16 MiB live Go heap and +10 goroutines relative to the first short-wave
cleanup. Samples follow forced Go GC and a two-second settle period. Every
native call must expose completed cleanup with handles_released=true,
shutdown_seen=true and callback_overflow=false. The in-process callback_overflow
field itself is not independently populated; successful global runtime close
also checks the native callback queue overflow flag. No active jobs may remain.
Global native disposal must return successfully after all waves.

The non-race worker is used for RSS measurements; a separate short run uses the
Go race detector. RSS is total process resident memory, not native allocator
usage alone. Handles are checked through per-room cleanup diagnostics, not an
independent count of every native allocation or OS handle. Growth bounds are
local regression criteria, not production capacity limits.

The extracted logs preserve test assertions and peer summaries without repetitive
ICE candidate logs. Raw-file hashes and process exit codes are in manifest.json;
resource samples and analysis.json retain the measurements. analyze.py regenerates
the aggregate from the raw output directory. Sources are preserved as .go.txt so
they do not become normal repository tests.

## Reproduction

Use the loopback server configuration and synthetic key pair in
[the earlier local results](../native-downstream-local-20261118/README.md).
Map these files into downstream app/ using Go overlay:

- helpers_test.go.txt → local_native_acceptance_test.go
- native_soak_test.go.txt → local_native_soak_test.go
- ../native-agent-events-20261118/evidence_test.go.txt → local_native_evidence_test.go

Build from agent-worker-golang:

```sh
go test -overlay /path/to/overlay.json -tags 'nativeinprocess nativeacceptance' ./app -run '^TestLocalNativeSoak$' -c -o /tmp/soak.test
```

Run from a temporary directory to avoid dotenv loading, with
NATIVE_ACCEPTANCE_URL=ws://127.0.0.1:17880, LIVEKIT_LIB_PATH pointing to the pinned
FFI dylib, and NATIVE_ACCEPTANCE_OUT pointing to a fresh output directory:

```sh
/tmp/soak.test -test.run '^TestLocalNativeSoak$' -test.v -test.timeout=16m
```

SOAK_SMOKE=1 selects six seconds followed by two eight-second waves. Build with
-race for the race smoke. For Pion comparison use comparison_soak_test.go.txt and
SOAK_PION=1; it uses the default session transport and allows one second after
readiness for Pion publication before sending synthetic captions. For the
receiver-ready diagnostic, use ready_soak_test.go.txt and SOAK_REPEAT_ONLY=1;
it waits for five returned tone frames and runs six seconds plus 12 eight-second
waves. The callback hook in helpers_test.go.txt is installed before connecting.

Temporary raw logs, binaries, overlays and output remain in
/private/tmp/native-soak-20261118/. No STG credentials or calls were used.

## Measurement limits

The two transport runs overlap on the same laptop/server, with separate worker
and caller processes. RSS can fall with macOS reclamation; comparisons describe
observed residency, not exclusive load benchmarks or native allocation counts.
The 10-minute workload does not replace an hours-long deployment soak.
No recording is enabled in this fixture; longer recording isolation/continuity
remains outside this result. Cross-call audio detection is dominant-tone based,
so it cannot rule out every low-level contaminating sample.

The initial Pion smoke failed a native-specific frame-count threshold and missed
one startup caption. Pion uses 50 ms PCM input frames versus native 20 ms. The
comparison corrects the threshold and waits one second before synthetic text;
that wait is fixture-specific and does not establish that immediate-start Pion
captions are reliable. No product fix is claimed.
