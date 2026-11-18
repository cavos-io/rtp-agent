# Downstream local drain and short soak — 2026-11-18

Upstream revision: `c0c411bbb`. Downstream base: `f87d99c2`, with the existing
uncommitted native integration. Host: macOS arm64; FFI 0.12.80; LiveKit 1.13.11.
Pion remains default. No downstream source/configuration edits or commits were
made. Go overlay added a temporary test to the downstream app package.

## Scope

The fixture uses the downstream server factory, native transport factory and
native hold adapter, actual registered worker/CLI signal handling, real native
connections, synthetic 600/1000Hz caller publishers, an echo assistant, and
recording. Two rooms run in one worker process under the race detector. Each run
sends real SIGTERM to its own process while calls remain active.

This is a downstream-component integration test, not `app.Run` end to end. It
bypasses dispatch snapshots, application provider wiring, SIP, transfer manager,
external storage/database services and session-end business finalizers. Recording
used the default Ogg build, not the downstream production FFmpeg image. The local
server was stopped after testing. No STG calls or real credentials were used.

## Results

| Scenario | Result |
| --- | --- |
| Two calls, 120-second media run, then SIGTERM with calls ending during grace | PASS; process exit 0, 6,001 input frames per room at the 120s sample, 5,998/5,999 returned output markers; no inbound cross-room marker assertion failures |
| Graceful drain | PASS in this fixture; both jobs completed, active jobs 0, runtime disposal returned successfully; 104 additional frames during approximately 2.13s signal-to-drain interval |
| Resource samples | 12 samples: 235–239 goroutines, Go heap 12.6–23.4 MB; final active sample 17.1 MB. Short observation only, no native RSS/leak certification |
| Ordinary recordings | Both decode; 122.02s and 121.96s. Expected tone present throughout checked interior one-second windows; zero alien-dominant windows |
| Active hold plus SIGTERM | FAIL; calls and recordings finalized, but hold cleanup returned `native room closed` |
| Five-second forced drain timeout | FAIL under race detector; timeout triggered, two jobs closed and runtime disposed, but report serialization raced with recording metadata finalization |
| Existing downstream teardown/native regressions, three repetitions | PASS; does not negate the integration failures |

See JSONL summaries, `recording-analysis.json`, `downstream-regressions.log`, and
`run-manifest.json`. The marker analysis skips partial startup/end windows; it
is not a perceptual audio-quality or transfer-recording test. Echo output is
observed by each publisher; full output/data/caption cross-room isolation is not
claimed. Go heap samples do not include the native library's allocations.

## Findings

### Hold cleanup after transport closure

The native room is marked closed by `JobContext`'s stop-publishing path before
session pre-close callbacks. The fixture closes the downstream hold adapter in
such a callback; its `EndHold` call then returns `native room closed`. Real
handles still released with no callback overflow, but cleanup is not clean.
The production downstream uses deferred hold closure, so this result establishes
a teardown-order/API-contract concern; it is not a complete production transfer
reproduction. Decide whether hold must stop before transport closure or whether
already-closed cleanup should succeed, then regression-test that contract.

Sources: `interface/worker/job.go` shutdown drain ordering,
`interface/worker/livekit/nativeroom/room.go` BeginShutdown,
`interface/worker/livekit/nativeroom/call_control.go` nativeControl, and downstream
`adapter/livekit/sessionmgr/native_hold_audio.go` Stop/Close.

### Forced-drain session-report race

Five race reports overlap `SessionReport.ToDict`/`saveSessionReport` in
`ExecuteLocalJobWithOptions` with `RecorderIO.PopulateSessionReport` invoked by
`finishJobLifecycle`/`uploadJobSessionReport`. The local job returns to save its
report while background finalization can still mutate recording metadata.
This also appeared in the canceled fake-job fixture attempt. See
`forced-report-races.txt` and `fake-job-report-race.txt`.

This is a local-job/report-lifecycle race in shared worker code, not evidence that
the native codec is faulty. Production dispatch uses a different entry path;
production applicability needs confirmation. It blocks claiming this local
forced-drain gate passed. It is separate from the previously excluded STG
transcript-cleanup warning messages.

## Reproduction and retained attempts

`local_native_acceptance_test.go.txt` preserves the temporary Go test. Map it with
Go's `-overlay` Replace entry to `agent-worker-golang/app/local_native_acceptance_test.go`,
then compile from that repository:

```sh
go test -overlay /path/to/overlay.json -race -tags 'nativeinprocess nativeacceptance' ./app -run '^TestLocalNativeActiveDrainAcceptance$' -c -o /tmp/native-acceptance.test
```

Run that executable from a temporary working directory with:

- `NATIVE_ACCEPTANCE_URL=ws://127.0.0.1:17880`
- `NATIVE_ACCEPTANCE_OUT` set to a fresh directory
- `LIVEKIT_LIB_PATH` set to the pinned macOS FFI library
- `NATIVE_ACCEPTANCE_HOLD=1` for the ten-second active-hold run
- `NATIVE_ACCEPTANCE_FORCE=1` for the ten-second run followed by forced drain
- Neither case flag for the 120-second ordinary soak

Use `-test.run '^TestLocalNativeActiveDrainAcceptance$' -test.timeout=180s -test.v`.
The fixture's local server uses the synthetic devkey/local-native-receive-experiment-secret
pair, TCP 17880/17881 and UDP 17900–17950; worker health port is 17882.
Original commands/configuration, logs, binaries and recordings are retained under
`/private/tmp/native-downstream-20261118/`. Raw log hashes are in the manifest.

An initial build included an unrelated legacy helper test and failed compilation;
it was removed from the fixture. Initial execution collided with an occupied
health port; the fixture was assigned a dedicated port. A second attempt used
fake-job caller/agent identity equivalence, preventing native discovery; it was
corrected to a reference connect job with distinct agent identity. These attempts
are not counted as native media or drain passes. The failed fake-job attempt was
terminated after it remained running; successful and failed completed cases have
separate outputs.

The initial validation recorded both failures before any fixes. The follow-up
below resolves and reruns those cases. Full application dispatch/storage finalization,
provider-backed active transfers, Linux media and a sustained concurrent-call
soak with RSS and repeated teardown remain open.


## Follow-up fixes and rerun

The commit containing this section fixes two lifecycle boundaries:

- Custom transport jobs preserve call controls until session cleanup finishes.
  Native Room no longer closes itself from an early session pre-close callback;
  job teardown owns closure after all session pre-close callbacks. Direct room
  users must still close their room/job explicitly. Downstream runner now
  registers hold.Close as session pre-close cleanup and retains deferred Close
  as a fallback. That downstream change remains uncommitted.
- Local report saving waits for the owning background finalizer, including when
  another goroutine won finishJob. The foreground server drain remains bounded.
  The additional local report wait uses the existing 15-second local close bound;
  if exceeded, it returns an explicit error without serializing a partial report.

Regression tests reproduced both ordering failures and early report saving before
fixes. The new tests also cover disconnected Pion ordering and a finalizer that
never settles within the report wait. Focused worker race tests passed three times.
The full default-build worker suite passed. Native room/runtime and CLI race
suites passed. Downstream app/session-manager
native and cleanup tests passed after registering hold cleanup.

| Rerun | Result |
| --- | --- |
| Active hold, two native calls, SIGTERM | PASS, exit 0, no race reports. Hold cleared successfully after 603 frames / 12.06s. Drain 2.084s, 101 frames continued during grace, both jobs ended, active jobs 0, handles released, no callback overflow, runtime disposed. |
| Two native calls, SIGTERM, five-second forced deadline | PASS, exit 0, no race reports. Drain 5.025s, 251 frames continued, both jobs ended, active jobs 0, handles released, no callback overflow, runtime disposed. |
| Recordings | All four decode: hold 11.94/12.00s; forced 14.94/14.94s. Expected markers in checked interior windows, zero alien-dominant windows. |
| Full worker race suite | FAIL on separate logger replacement and availability/assignment races; selected failures reproduced with HEAD job.go/server.go via overlay. These are retained in native-drain-worker-suite.log and native-drain-baseline.log, not claimed fixed. |

An intermediate rerun (`hold-fixed`) still failed because native Room itself
registered Close before the application's hold callback. Removing that premature
callback, in addition to fixing JobContext's custom transport detection, produced
the passing `hold-fixed-v2` run. The failure remains in the manifest.

The fixture's synthetic tokens cannot update participant metadata, so native
client-state warnings remain; these runs do not certify that publication path.
The two-minute soak above was not repeated: the failing hold and forced cases
were rerun with two concurrent calls. Production app.Run, provider/SIP transfer
teardown, Linux live media, reconnect and sustained RSS soak remain unvalidated.

Commit hooks passed: test integrity, configured go-file-arch architecture gate,
and targeted default-build worker/native-room tests. Pion selection is unchanged.
