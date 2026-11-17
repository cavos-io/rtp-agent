# Helper fixture readiness follow-up

The lifecycle checkpoint's failed race runs and failed commit hooks remain unchanged under `../native-lifecycle-20261117/checkpoint-*`.

The affected tests launched freshly created shell executables and required complete protocol readiness within one second. On failure, some tests did not close the client before TempDir cleanup, allowing the helper to outlive its script. The resulting "No such file or directory" is secondary to the readiness failure.

Investigation:

- The unchanged graceful-stop test passed five consecutive isolated runs (`baseline.log`), demonstrating intermittent behavior rather than a deterministic protocol regression.
- Twelve separately instrumented startups measured shell entry, config read and ready emission (`startup-timing.json`). Shell entry took 66–604 ms; config read was nearly immediate and the external cat added only a few milliseconds. These probes locate normal startup variability before shell execution, but did not capture a >1-second failure. They do not establish whether scheduling, executable validation, or another host mechanism caused the failed launches.
- No native helper runtime, transport, or playout code changed for this follow-up.

Focused test-only fix in `examples/internal/nativeroom/client_test.go`:

- Separate fixture startup allowance (five seconds) from tested shutdown timing. Startup speed is not the contract these shutdown tests assert. The readiness barrier remains mandatory.
- Surface an early child exit immediately, including its error.
- Register client cleanup before waiting so timeout/failure reaps it before removing its temporary script.
- Keep all existing forced-kill, nonzero-exit, diagnostics-drain, capture/stop ordering and post-close assertions, including shutdown deadlines.
- Add explicit missing-readiness/early-exit checks and an actual delayed-start shell fixture (1.1 seconds) that must still shut down gracefully and report cleanup.

The full tagged adapter race suite passed (`fixed-race.log`). Repeated focused tests and commit-hook results are recorded separately. Previously reported broad core-agent race failures remain outside this test-fixture fix.
