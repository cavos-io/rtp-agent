# Memory-Leak and Lifecycle Hardening Design

## Objective

Complete the `haikal/fix/memory-leak` work by addressing the shared causes of
goroutine retention and shutdown deadlocks, while preserving observable behavior
from `refs/agents/livekit-agents`.

This work is limited to the lifecycle paths already exercised by the branch:

- agent-session startup, shutdown, restart, and event delivery
- LLM, STT, TTS, tokenization, and transcript-synchronizer shutdown
- LiveKit transcription writer ordering
- LiveKit running-job completion ordering

Pre-existing data races in unrelated fallback adapters are not part of this
change. They must be tracked separately because they prevent a repository-wide
`go test -race` gate from being clean.

## Reference Behavior

The Python reference provides the behavioral contract through three mechanisms:

1. `utils.aio.Chan` is unbounded by default. Internal `send_nowait` operations do
   not wait for downstream consumers.
2. Streaming components own asyncio tasks. Their `aclose` methods close inputs,
   cancel tasks, and await termination of the entire owned task tree.
3. `AgentSession.start` and `_aclose_impl` execute under one lifecycle lock.
   Session shutdown closes session-owned activities and I/O, while reusable
   agent/model providers remain owned by the agent or application.

Relevant reference files:

- `livekit/agents/utils/aio/channel.py`
- `livekit/agents/tokenize/token_stream.py`
- `livekit/agents/voice/generation.py`
- `livekit/agents/voice/transcription/synchronizer.py`
- `livekit/agents/tts/stream_adapter.py`
- `livekit/agents/stt/stt.py`
- `livekit/agents/voice/agent_session.py`
- `livekit/agents/voice/room_io/_output.py`
- `livekit/agents/ipc/job_proc_lazy_main.py`

## Confirmed Go Gaps

### Incomplete event-send cancellation

The branch makes secondary subscriber sends observe session teardown, but primary
lossless event sends still block indefinitely when their bounded channel is full.
This affects speech, conversation, tool, metrics, usage, error, DTMF, and close
event families.

The active teardown channel is also read without the session mutex while restart
replaces it under the mutex, creating an avoidable synchronization gap.

### Fragmented session lifecycle serialization

`starting`, `startDone`, `started`, and `teardownCh` collectively approximate the
single lifecycle lock used by Python. The branch's direct wait on `startDone`
ignores caller cancellation. Failed startup and restart also manipulate teardown
generation separately from the lifecycle transition.

### Incorrect detector ownership

`AudioTurnDetector` is stored on the reusable `Agent`. Closing it from
`AgentSession.Stop` permanently closes implementations such as Cavos SmartTurn's
gRPC connection, leaving a restarted session with a closed provider.

### Incomplete TTS child cleanup

The TTS stream adapter can return from its run loop after synthesis failure while
its input-forwarding goroutine remains blocked on `inputCh`. Python cancels and
awaits both adapter tasks in a `finally` block.

### Synchronizer lacks a pre-start close signal

`TranscriptSynchronizer.done()` returns a nil channel before a run context exists.
A producer that fills a channel before startup or after failed startup has no
close signal capable of releasing it.

## Design

### 1. Serialize session lifecycle with a cancellable gate

Replace cross-operation waiting on `starting/startDone` with one session lifecycle
gate. `StartWithOptions` and `Stop` acquire the gate with their caller context and
hold it for the complete state transition.

The gate has these semantics:

- concurrent start waits for the current transition, or returns `ctx.Err()`
- stop during start waits for startup and then tears it down, matching Python's
  lifecycle lock
- a cancelled stop that never acquires the gate does not claim to have stopped
  the eventual run
- teardown generation is created, published, and closed while the lifecycle
  transition owns the gate
- failed startup closes its run teardown generation before returning

Existing public method signatures remain unchanged.

### 2. Centralize lossless event delivery

Introduce one small generic helper for lossless channel delivery:

```go
sendUntil(ch, value, runDone) bool
```

It attempts an immediate send, then waits for either channel capacity or the
captured run teardown signal. All primary and secondary lossless event sends use
this helper. Existing best-effort events continue using non-blocking sends.

Subscriber lists and the current teardown signal are snapshotted together under
the session mutex before delivery. No user-controlled channel operation occurs
while holding that mutex.

This preserves the current Go lossless-event contract while ensuring teardown can
release every blocked producer. It intentionally does not add an unbounded queue:
that would be a broader API and memory-policy change than the branch requires.

### 3. Restore resource ownership boundaries

Remove `AudioTurnDetector.Close` from `AgentSession.Stop`. The session continues to
close only resources it constructs or owns for that run.

Reusable providers remain owned by the `Agent` or application composition root.
`App.Close` will close its configured detector once when the application is
permanently shut down. Callers constructing `Agent` directly retain the same
ownership obligation for their providers. No detector factory or reconnect
abstraction is added.

### 4. Make stream adapters own their complete goroutine tree

For TTS stream adaptation:

- cancel the wrapper context on every run-loop exit
- make each helper goroutine observe wrapper cancellation
- track helper completion and wait before closing `doneCh`
- close output channels only after all producers have stopped
- keep `Close` idempotent and wait for `doneCh`

The existing STT change already follows this pattern closely. Its tests will be
expanded only if the shared audit finds a producer that can outlive `Close`.

### 5. Give transcript synchronization a lifetime cancellation signal

Create the synchronizer lifetime context during construction rather than startup.
Every potentially blocking push or output send observes that signal. Close and
cancel are safe before start, during a run, and after failed startup.

Run-specific timing state remains initialized by `Start`; lifetime cancellation
does not change transcript timing behavior.

### 6. Preserve correct branch changes

Keep the branch's guarded LiveKit text writer. It mirrors Python's serialized
`capture_text`/`flush` writer lifecycle and avoids holding `RoomIO` state locks
during SDK I/O.

Keep job `MarkDone` ordering before result observation. The nil `ShutdownDone`
case remains a Go API safety rule; production `AgentServer` callers provide a
non-nil shutdown signal.

## Error and Cancellation Semantics

- Failure to acquire the lifecycle gate returns the caller context error.
- Cancellation after a stream has been explicitly closed continues to normalize
  to the component's existing EOF/closed result.
- Teardown-caused event-send cancellation is silent because session shutdown has
  made delivery obsolete.
- Provider close errors remain reported by the owning layer; session shutdown no
  longer reports errors from agent-owned audio turn detectors.

## Test Strategy

Follow test-driven development for every production change. Each regression test
must fail on the current branch for the intended reason before implementation.

Add or update deterministic tests for:

- blocked primary event delivery released by teardown
- blocked secondary event delivery released by teardown
- lifecycle-gate cancellation for concurrent start and stop
- stop-after-start ordering
- failed startup closing its teardown generation
- closeable audio detector remaining usable after session restart
- application shutdown closing an application-owned audio detector exactly once
- TTS synthesis failure terminating all adapter helper goroutines
- transcript synchronizer cancellation before startup
- guarded RoomIO writer ordering and running-job completion ordering

Prefer handshake channels over sleeps. Use `goleak` only in focused tests whose
owned goroutines can be isolated reliably. Run new concurrency tests repeatedly
with `-count=50` and under `-race`.

## Parity Evidence

Update existing TSV rows rather than representing Go scheduler details as new
cross-runtime cases. Rows will cite the exact Python lifecycle mechanism:

- unbounded `aio.Chan` and `send_nowait`
- task cancellation and `aclose`
- `AgentSession` lifecycle lock
- serialized RoomIO writer flush
- job task completion and shutdown future ordering

Use `go-test` for Go-specific deadlock, mutex, and goroutine contracts. Use a
cross-runtime or JSON scenario only when both runtimes can emit the same stable
lifecycle trace.

## Validation

Run, in order:

1. focused new regression tests
2. repeated focused tests with `-count=50`
3. focused packages with `-race`
4. focused parity cases through `scripts/parity-gate.sh --case ...`
5. `scripts/go-test-all.sh`
6. `scripts/go-build-all.sh`
7. `go-arch-lint check`
8. staged test-integrity and analyzer gates
9. full `scripts/parity-gate.sh`

The shared `refs/` fixture directory must be linked into the worktree before full
tests. Any pre-existing repository-wide race failures will be reported separately
and will not be described as fixed by this branch.

## Non-Goals

- replacing all native channels with a Go implementation of Python `aio.Chan`
- fixing unrelated fallback-adapter races
- changing public event-channel APIs
- adding provider factories solely for restart
- changing reference Python code
