# Native primary playback recovery — 2026-11-16

Opt-in adapter only. Default transport, helper protocol, receive playout policies,
provider adapters and adaptive-interruption policy unchanged. Pending PCM blocks
in its producer rather than accumulating in a new application queue. Producer
serialization is cancellation-aware; pause does not prevent clear or shutdown.

## Evidence and acceptance

Go regression tests cover exact pending PCM order/content and partial tail,
idempotent controls, capture-before-clear ordering, discarded queue accounting,
paused playout wait, producer cancellation, shutdown, hard interruption rejecting
old content, helper error visibility and real AgentActivity false-interruption
onset/end events resuming the same SpeechHandle. Tests use fake IPC for controller
contracts, not as native engine evidence.

Live evidence uses the real race-instrumented Go helper/native engine and starter
session wiring, a local server and an Opus-decoding caller. Gates were set before
interpreting the run: two participants; 250ms maximum input callback gap and
PCM/wall difference (total and each one-second window); at least20 decoded frames
of each output marker; no marker growth during a200ms paused observation after
300ms network/encoder settling; resumed pending publication completes; helper
reaped with exit0, no forced kill and all native cleanup flags. Existing drain,
hard-clear and lifecycle gates were retained.

| Trial | PCM / wall seconds | Max input gap | Output frames 990 / 1320Hz | Result |
|---|---:|---:|---:|---|
| live-01: pause with no queued source | 5.000 / 5.000074 | 53.36ms | 50 / 54 | Pass |
| live-02: queue output, pause, resume | 5.000 / 5.000240 | 83.91ms | 50 / 54 | Pass |

Both had two participants, no paused marker growth, preserved resumed content and
clean helper exits. The second trial's maximum one-second PCM/wall difference
was0.912ms; helper reap took1.020s including race-runtime exit delay. Separate
session-close, publisher-disconnect and track-unpublish live regressions passed.
No race was reported in these live tests or the native adapter regression tests.
This does not measure exact acoustic pause latency or prove Python bit parity.

Platform: macOS27.2.1 ARM64, Go1.27.4, local LiveKit server1.13.11. Unchanged pinned
native FFI0.12.80, rust-sdks5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb.

## Validation and preserved failures

Artifacts: `examples/native-receive-probe/results/primary-pause-20261116/`.
The pre-run SHA256 manifest covers3079 earlier artifact files. No old result was
replaced. Both live trials, lifecycle logs, guard output and failed checks remain.

- Playback-focused race suite passed5 repetitions; full nativeroom and starter
  race suites passed on recheck. Stalled-consumer cleanup passed5 isolated repeats.
- Initial full native package repeat run had one existing
  `TestClientReapsStalledConsumer` timeout at6s while a live test also ran. Cause
  is unproven; preserved in `package-initial-failure.txt`, not dismissed as fixed.
- Broader core recovery race selection found an existing mock-counter race:
  `TestAgentActivitySpeechDoneClearsPausedFalseInterruption` reads `resumeCount`
  at agent_activity_test.go:127 while the asynchronous done callback writes it
  at :8334. Core and that mock are unchanged. See `core-recovery-race.log` for
  separate follow-up; no full core race-suite pass is claimed. The same selected
  core recovery tests pass without race instrumentation (`core-recovery.log`).
- The manifest runner treats the test name as literal. An initial regex selector
  ran no tests; preserved in `parity.log`. Corrected exact case passed in
  `parity-recheck.log` (Go-test evidence only).
- Architecture, vet, explicit-file test-integrity and targeted analyzer checks
  passed. No staged-only guard coverage is claimed. The previously documented
  parity wrapper argument bug remains; full parity-suite pass is not claimed.

## Reproduce

Use a fresh result directory for every live invocation. Start the existing local
server configuration under a new artifact directory; no paid providers required.
From the repository root:

```sh
go test -race ./examples/internal/nativeroom -run 'Test(Native|Playback|Clear|Publish|Old)' -count=5
go -C examples/native-receive-probe build -race -o .cache/probe-primary-pause .
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/primary-pause-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary-pause" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestStarterNativePrimaryPause$' -count=1 -timeout 90s -v
```

Existing cloud commands and opt-in environment flags in NATIVE_ROOM.md still
apply; rebuild the agent to pick up the controller wiring. This change has not
yet received a cloud false-interruption conversation test. Five two-minute
sessions were not repeated for this output-only change; their earlier results
remain intact. Keep the native path opt-in.
