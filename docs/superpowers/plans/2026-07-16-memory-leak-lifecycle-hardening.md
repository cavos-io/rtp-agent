# Memory-Leak and Lifecycle Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the remaining goroutine-retention and shutdown-deadlock paths on `haikal/fix/memory-leak` while matching the vendored LiveKit Python lifecycle contracts.

**Architecture:** Keep native Go channels and public APIs, centralize cancellation-aware event delivery, serialize session start/stop through one context-aware gate, and make streaming components cancel and await owned goroutines. Reusable audio-turn detectors move from session ownership to application ownership.

**Tech Stack:** Go 1.26, contexts/channels/synchronization, `go.uber.org/goleak`, TSV parity manifest, vendored Python reference.

## Global Constraints

- Do not edit `refs/agents/*`.
- Preserve architecture dependency direction.
- Verify every production change with a failing test first.
- Keep scheduler-specific evidence as `go-test`.
- Prefer handshake channels over sleeps.
- Do not introduce unbounded queues or change public event APIs.
- Keep unrelated fallback-adapter races out of scope.

---

### Task 1: Restore baseline parity gates

**Files:**
- Modify: `adapter/ultravox/plugin.go`
- Modify: `adapter/ultravox/plugin_test.go`
- Modify: `core/beta/workflows/dtmf_inputs.go`
- Test: `adapter/ultravox/plugin_test.go`
- Test: `core/beta/workflows/dtmf_inputs_test.go`

**Interfaces:**
- Consumes: Ultravox reference version `1.5.19.rc1`; `parseDtmfInputs([]string)`.
- Produces: matching plugin metadata; split phrase `number key` normalizes to `#`.

- [ ] **Step 1: Verify RED**

```bash
go test ./adapter/ultravox -run '^TestUltravoxPluginMetadataMatchesReference$' -count=1
go test ./core/beta/workflows -run '^TestRecordInputsToolNormalizesSpokenNumberKey$' -count=1
```

Expected: version-prefix failure and incomplete DTMF result.

- [ ] **Step 2: Align Ultravox production and test metadata**

```go
PluginVersion = "1.5.19.rc1"
```

- [ ] **Step 3: Preserve `number key` before generic filler removal**

```go
if token == "number" && i+1 < len(tokens) &&
	isDtmfFiller(tokens[i+1]) && normalizeDtmfToken(tokens[i+1]) != "key" {
	continue
}
```

- [ ] **Step 4: Verify GREEN**

```bash
go test ./adapter/ultravox -run '^TestUltravoxPluginMetadataMatchesReference$' -count=1
go test ./core/beta/workflows -run 'TestRecordInputsToolNormalizes(SpokenNumberSign|SplitSpokenNumberSign|SpokenNumberKey)$' -count=5
```

Expected: PASS.

- [ ] **Step 5: Commit baseline fixes, design, and plan**

```bash
git add adapter/ultravox/plugin.go adapter/ultravox/plugin_test.go core/beta/workflows/dtmf_inputs.go docs/superpowers
git commit -m "fix(parity): restore branch baseline"
```

### Task 2: Serialize session lifecycle transitions

**Files:**
- Modify: `core/agent/agent_session.go`
- Modify: `core/agent/agent_session_stop_race_test.go`
- Modify: `core/agent/agent_session_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Consumes: `StartWithOptions(context.Context, StartOptions)`, `Stop(context.Context)`.
- Produces: `acquireLifecycle(context.Context) error` and serialized start/stop transitions.

- [ ] **Step 1: Add failing tests**

Add `TestStopDuringStartHonorsContext`, `TestConcurrentStartHonorsContext`, and `TestFailedStartClosesRunTeardown`. Use a blocking assistant and handshake channels. The cancellation assertion is:

```go
ctx, cancel := context.WithCancel(context.Background())
cancel()
if err := s.Stop(ctx); !errors.Is(err, context.Canceled) {
	t.Fatalf("Stop error = %v, want context.Canceled", err)
}
```

- [ ] **Step 2: Verify RED**

```bash
go test ./core/agent -run 'Test(StopDuringStartHonorsContext|ConcurrentStartHonorsContext|FailedStartClosesRunTeardown)$' -count=1
```

Expected: cancellation is ignored or teardown remains open.

- [ ] **Step 3: Add the context-aware gate**

Initialize `lifecycleGate: make(chan struct{}, 1)` and add:

```go
func (s *AgentSession) acquireLifecycle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.lifecycleGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *AgentSession) releaseLifecycle() { <-s.lifecycleGate }
```

Hold the gate across complete start and stop transitions. Remove `starting`, `startDone`, and their wait loops. Close failed run teardown generations.

- [ ] **Step 4: Verify GREEN**

```bash
go test ./core/agent -run 'Test(StopDuringStart|ConcurrentStart|FailedStart|EmitAgentOutputTranscribedDeliversAfterRestart)' -count=50
go test -race ./core/agent -run 'Test(StopDuringStart|ConcurrentStart|FailedStart)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update parity notes and commit**

Update `agent-session-stop-waits-for-starting` to cite Python's `async with self._lock`.

```bash
git add core/agent/agent_session.go core/agent/agent_session_stop_race_test.go core/agent/agent_session_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(agent): serialize session lifecycle"
```

### Task 3: Release all blocked lossless event producers

**Files:**
- Modify: `core/agent/agent_session.go`
- Modify: `core/agent/agent_session_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Produces: `sendUntil[T](chan T, T, <-chan struct{}) bool`.

- [ ] **Step 1: Add a failing table test**

Fill each subscribed primary lossless event channel, start one additional emit, stop the session, and require the emitter handshake to complete.

- [ ] **Step 2: Verify RED**

```bash
go test ./core/agent -run '^TestLosslessPrimaryEventEmitUnblocksOnStop$' -count=1
```

Expected: a direct `primary <- ev` remains blocked.

- [ ] **Step 3: Centralize lossless delivery**

```go
func sendUntil[T any](ch chan T, ev T, done <-chan struct{}) bool {
	select {
	case ch <- ev:
		return true
	default:
	}
	select {
	case ch <- ev:
		return true
	case <-done:
		return false
	}
}
```

Snapshot subscriber channels and the active teardown signal under the same mutex. Use the helper for all lossless primary and secondary sends; retain non-blocking interim/state behavior.

- [ ] **Step 4: Verify and commit**

```bash
go test ./core/agent -run 'Test(LosslessPrimaryEventEmitUnblocksOnStop|EmitAgentOutputTranscribedUnblocksOnStop)' -count=50
go test -race ./core/agent -run 'Test(LosslessPrimaryEventEmitUnblocksOnStop|EmitAgentOutputTranscribedUnblocksOnStop)' -count=1
git add core/agent/agent_session.go core/agent/agent_session_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(agent): release event producers on teardown"
```

Expected: PASS.

### Task 4: Restore audio-turn-detector ownership

**Files:**
- Modify: `core/agent/agent_session.go`
- Modify: `core/agent/agent_session_test.go`
- Modify: `app/app.go`
- Modify: `app/app_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Consumes: `Agent.AudioTurnDetector`, `App.Close(context.Context)`.
- Produces: restart-safe detector; application close closes it exactly once.

- [ ] **Step 1: Add failing ownership tests**

Add `TestAgentSessionRestartKeepsAudioTurnDetectorOpen` using a detector that rejects prediction after close. Add `TestAppCloseClosesAudioTurnDetectorOnce` and call `App.Close` twice.

- [ ] **Step 2: Verify RED**

```bash
go test ./core/agent -run '^TestAgentSessionRestartKeepsAudioTurnDetectorOpen$' -count=1
go test ./app -run '^TestAppCloseClosesAudioTurnDetectorOnce$' -count=1
```

Expected: session closes the reusable detector; app does not own closure.

- [ ] **Step 3: Move closure to application ownership**

Remove detector closure from session stop. Add a `sync.Once`-guarded detector closure in `App.Close`, joining it with telemetry shutdown errors.

- [ ] **Step 4: Verify and commit**

```bash
go test ./core/agent -run 'TestAgentSession(RestartKeepsAudioTurnDetectorOpen|Stop)' -count=10
go test ./app -run '^TestAppCloseClosesAudioTurnDetectorOnce$' -count=10
git add core/agent/agent_session.go core/agent/agent_session_test.go app/app.go app/app_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(app): own audio turn detector lifetime"
```

Expected: PASS.

### Task 5: Drain the TTS adapter task tree

**Files:**
- Modify: `core/tts/stream_adapter.go`
- Modify: `core/tts/stream_adapter_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Produces: every `run` exit cancels and waits for helper goroutines.

- [ ] **Step 1: Add a failing leak test**

Add `TestStreamAdapterSynthesisErrorStopsInputForwarder` with a synthesis-error fake, a helper-exit handshake, and focused `goleak.VerifyNone(t)`.

- [ ] **Step 2: Verify RED**

```bash
go test ./core/tts -run '^TestStreamAdapterSynthesisErrorStopsInputForwarder$' -count=1
```

Expected: input forwarder remains blocked.

- [ ] **Step 3: Cancel and await helpers**

Track helpers with `sync.WaitGroup`. Ensure exit order cancels context, waits helpers, then closes outputs and `doneCh`. Every helper observes `w.ctx.Done()`.

- [ ] **Step 4: Verify and commit**

```bash
go test ./core/tts -run 'TestStreamAdapter(SynthesisErrorStopsInputForwarder|CloseWhileSpeakingDoesNotDeadlockOrLeak)' -count=50
go test -race ./core/tts -run 'TestStreamAdapter(SynthesisErrorStopsInputForwarder|CloseWhileSpeakingDoesNotDeadlockOrLeak)' -count=1
git add core/tts/stream_adapter.go core/tts/stream_adapter_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(tts): drain stream adapter tasks"
```

Expected: PASS.

### Task 6: Close transcript synchronization before start

**Files:**
- Modify: `core/agent/transcription.go`
- Modify: `core/agent/transcription_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Produces: constructor-created lifetime cancellation signal.

- [ ] **Step 1: Add failing pre-start test**

Fill text input before starting, block one push, cancel the synchronizer, and require the push handshake to return.

- [ ] **Step 2: Verify RED**

```bash
go test ./core/agent -run '^TestTranscriptSynchronizerPushUnblocksWhenCanceledBeforeStart$' -count=1
```

Expected: `done()` is nil and the push remains blocked.

- [ ] **Step 3: Initialize lifetime cancellation in the constructor**

Create the synchronizer context and cancel function in `NewTranscriptSynchronizer`; do not replace it at start.

- [ ] **Step 4: Verify and commit**

```bash
go test ./core/agent -run 'TestTranscriptSynchronizer' -count=20
go test -race ./core/agent -run 'TestTranscriptSynchronizer' -count=1
git add core/agent/transcription.go core/agent/transcription_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(agent): close transcript sync before start"
```

Expected: PASS.

### Task 7: Final validation

**Files:**
- Modify only if validation exposes a defect in files above.

- [ ] **Step 1: Run focused packages**

```bash
go test ./library/tokenize ./core/tts ./core/stt ./core/agent ./interface/worker/livekit ./app
```

Expected: PASS.

- [ ] **Step 2: Run every changed parity case**

Use `scripts/parity-gate.sh --case <name>` for each changed TSV row.

Expected: PASS.

- [ ] **Step 3: Run broad gates**

```bash
scripts/go-test-all.sh
scripts/go-build-all.sh
go-arch-lint check
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
scripts/parity-gate.sh
```

Expected: PASS. Report unrelated pre-existing race findings separately.

- [ ] **Step 4: Inspect final state**

```bash
git status --short
git diff origin/haikal/fix/memory-leak...HEAD --stat
git log --oneline origin/haikal/fix/memory-leak..HEAD
```

Expected: clean worktree and focused commits.
