# Recursive worker race follow-up — 2026-11-18

Base: 422761bb1. Host: macOS arm64. Pion remains the runtime default.
Downstream agent-worker-golang was neither edited nor committed in this task;
its HEAD remains f87d99c2. Previous failures are retained in
[the baseline report](../worker-races-20261118/README.md).

## Changes

- IPC starts a command before publishing its Process to Close and ping readers.
  Launch now returns process-start errors immediately. Close waits for launch
  preparation with its context bound; a closed executor cannot start a late child.
- Process completion cancels and joins the ping task before closing done. This
  prevents late ping activity after a successful Close/cleanup boundary.
- IPC test ping tasks are explicitly canceled and joined before restoring test
  hooks. Ping bytes use synchronized storage; pool close counters are atomic.
  Blocked-kill tests also reap their real child before restoring globals.
- Agora transcript capture locks its shared payload storage. In-progress join
  tests await a channel that publishes fake client state rather than polling a
  context field written concurrently. No Agora production behavior was changed.
- Worker Run fixtures restore the LiveKit credential environment that Run exports
  for subprocesses. Initially absent variables are restored to absent by t.Setenv
  cleanup; repeated execution no longer inherits synthetic test credentials.

Added regression coverage includes failed process starts, bounded Close during
blocked launch preparation, and simultaneous Agora Close/Leave during join. The
clean-exit ping test now waits for an actual ping failure before closing, so the
failure assertion cannot be satisfied solely by Close marking status failed.

## Validation

Commands use the race detector without filtering or skipping tests:

```sh
go test -race ./interface/worker/... ./interface/cli -count=3
go test -race -tags nativeinprocess ./interface/worker/... ./interface/cli -count=3
```

The three repetitions occur in the same test process per package. This directly
exercises the environment leakage that previously required fresh processes.
The untagged initial recursive run passed, as did focused Agora/IPC suites with
new regression tests (three repetitions). Final logs are retained alongside this
report. These are local unit/integration suites; no claim of additional STG,
Linux live-media, hardware Agora SDK or sustained call soak validation is made.


Final result: **PASS for both recursive commands**, each with three repetitions
in the same process per package; no race reports and no environment-dependent
failures. `ffipb` contains no tests. Focused Agora/IPC regressions also passed three
repetitions. Default and native-tag logs preserve the complete package outcomes.
