# Worker logger and assignment races — 2026-11-18

Base: ebe3d436a. Host: macOS arm64. Pion remains default. No downstream files
were changed or committed in this task; downstream HEAD remains f87d99c2.

## Fixes

- Publish the pending assignment timer and map entry before its callback may
  access the timer identity. time.AfterFunc can run before returning, including
  with a positive duration. A channel establishes the required happens-before
  relationship without changing stale-timer matching or caller map locking.
- Retain the acceptance logger when scheduling its timeout, so a late timeout
  does not reread the replaceable process logger.
- Disconnected assignment/reload tests now await final status logging before
  inspecting captured errors and restoring the process logger. Active-job removal
  happens earlier and was not a valid completion barrier.
- Both asynchronous availability tests wait for their request callbacks to finish
  and close their servers before returning, avoiding a late read of the next test's
  modified assignment timeout and leaked acceptance timers. Repetition exposed the same gap in the load
  reservation fixture; both are now synchronized.

The immediate-timeout regression reproduced the timer race before the fix and
checks 1,000 timeouts per run for non-nil, matching timer identity and expiration.
The focused regression command passed ten repetitions. Assertions were retained;
no race detector suppression or global logger API replacement was introduced.
A retained-logger regression also checks timeout delivery after process logger
replacement. Failure/panic assignment tests now wait for teardown separately:
the failure status is intentionally sent before finalization.

## Validation scope

The original failing suite is the complete `./interface/worker` package.
A broader recursive run also tested `./interface/worker/... ./interface/cli`:
worker, LiveKit, native room/runtime and CLI passed, but unchanged Agora and IPC
packages exposed separate races. `recursive-summary.log` lists affected tests;
`recursive-race-sites.json` preserves deduplicated stack locations and the raw log
hash/path. The recursive subtree is **not** claimed race-clean. Findings include
Agora transcript test publisher access and join/leave test state, plus IPC process
start/close, ping lifecycle and pool test counters. These need a separate fix.

An additional in-process `-count=3` run exposed a late acceptance logger read
(fixed by retaining that logger) and credential/environment leakage between test
iterations. Three full-package runs passed in fresh processes. A subsequent native-tag run
exposed the failure-status-before-teardown assertion, corrected as described
above; its failed output is retained. Final full-package repetitions run in fresh processes. This does not
claim that every test supports repeated execution within one process.

Commands:

```sh
go test -race ./interface/worker -count=1
# repeated in three fresh processes
go test -race -tags nativeinprocess ./interface/worker ./interface/worker/livekit ./interface/worker/livekit/nativeroom ./interface/worker/livekit/nativeinprocess ./interface/cli -count=1
```

These are local automated checks, not additional STG, Linux live-media or soak
validation. Existing native promotion gates remain in force.


## Final results

| Check | Result |
| --- | --- |
| Complete default worker package, race detector, final source | PASS (10.843s); worker-race-default-final-v3.log |
| Complete native-tag worker, LiveKit, native room/runtime and CLI packages | PASS; worker-race-native-final-v3.log |
| Focused original races plus retained-logger regression | PASS, ten repetitions; worker-race-focused-final.log |
| Immediate timer regression | Reproduced before fix; passed ten repetitions after fix (1,000 expirations each) |
| Recursive worker subtree | FAIL in unchanged Agora/IPC packages; retained site summary, not a promotion pass |

Earlier logs are retained as intermediate findings, not final passes. No STG calls
or downstream image rebuild was needed for these worker lifecycle fixes.


## Follow-up

The Agora/IPC and repeated-run environment findings above are addressed by the
[next validation report](../worker-recursive-races-20261118/README.md). The failure
logs here preserve the earlier baseline; consult that report for final results.
