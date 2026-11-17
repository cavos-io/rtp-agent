# Checkpoint verification, 2026-11-17

All 3,576 existing artifact hashes matched before staging.

Fresh native wrapper race suite passed (3.869 s). Concurrent root adapter/starter race verification returned:

```text
--- FAIL: TestClientGracefulStopDrainsCleanup (1.81s)
    --- FAIL: TestClientGracefulStopDrainsCleanup/exit-7 (1.01s)
        client_test.go:182: not ready
/bin/sh: /var/folders/vz/3r21rz9x2q9c2x1msr9ljl7r0000gn/T/TestClientGracefulStopDrainsCleanupexit-73671822405/001/helper: No such file or directory
FAIL github.com/cavos-io/rtp-agent/examples/internal/nativeroom 17.056s
ok github.com/cavos-io/rtp-agent/examples/starter 2.255s
```

The existing shell-helper fixture has a one-second readiness gate. Its temporary directory is removed when that assertion fails, consistent with the subsequent shell error. Host scheduling is a possibility, not a proven root cause. No test timeout or helper behavior was changed. The isolated adapter rerun is preserved alongside this note.

The previously documented broader core-agent race failures remain separate follow-up work; this checkpoint does not establish full-suite race cleanliness.

The isolated adapter race rerun also failed the same exit-7 readiness gate (15.128 s). Treat this as unresolved, not a passing race verification. Commit hooks are run separately using their configured non-race tests.

Commit attempt blocked: test integrity and architecture passed, but targeted non-race tests failed helper readiness in TestCleanupDiagnosticsReportsForcedKill, TestClientGracefulStopDrainsCleanup/exit-0, and TestClientStopOrdersAfterInFlightCapture. Hooks were not bypassed. Changes remain staged for follow-up.
