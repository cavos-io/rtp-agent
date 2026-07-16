# Review Deadlock Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the AgentSession startup/state-delivery deadlock and RoomIO in-flight text-write shutdown deadlock without weakening lossless delivery or post-close ordering.

**Architecture:** End exclusive session lifecycle ownership before blocking observer notification, while keeping all startup state mutations serialized. Split RoomIO closed-state locking from write serialization so close can invoke the underlying cancellation mechanism during an active write.

**Tech Stack:** Go 1.26, contexts, channels, mutexes, LiveKit SDK text streams, TSV parity manifest.

## Global Constraints

- Do not edit `refs/agents/*`.
- Preserve lossless subscribed event delivery during an active run.
- Do not signal session teardown before `Stop` acquires lifecycle ownership.
- Do not permit a new RoomIO write to begin after writer closure.
- Verify each production change with a failing test first.
- Use deterministic handshakes rather than sleeps for deadlock reproduction.

---

### Task 1: Release lifecycle ownership before state notification

**Files:**
- Modify: `core/agent/agent_session.go`
- Modify: `core/agent/agent_session_stop_race_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Consumes: `StartWithOptions(context.Context, StartOptions)`, `Stop(context.Context)`, `UpdateAgentState(AgentState)`.
- Produces: startup serialization ending before the lossless listening-state subscriber send.

- [ ] **Step 1: Write the failing regression**

Create `TestStopUnblocksListeningStateNotificationDuringStart`. Subscribe through `AgentStateChangedEvents`, fill its capacity, start a configured fake session, wait until `started` is true, then call `Stop`. Require `Stop` and `Start` to return before `testTimeout()`.

- [ ] **Step 2: Verify RED**

```bash
go test ./core/agent -run '^TestStopUnblocksListeningStateNotificationDuringStart$' -count=1 -timeout 10s
```

Expected: timeout because `StartWithOptions` holds `lifecycleGate` while `UpdateAgentState` waits on the full subscriber and `Stop` waits for the gate.

- [ ] **Step 3: Move the lifecycle release boundary**

After all started-run fields and background loops are installed, release the gate before the final call:

```go
s.releaseLifecycle()
lifecycleHeld = false
s.UpdateAgentState(AgentStateListening)
```

Keep validation and startup failure paths under the gate.

- [ ] **Step 4: Verify GREEN and parity**

```bash
go test ./core/agent -run 'Test(StopUnblocksListeningStateNotificationDuringStart|StopDuringStart|ConcurrentStart|FailedStart)' -count=50
go test -race ./core/agent -run 'Test(StopUnblocksListeningStateNotificationDuringStart|StopDuringStart|ConcurrentStart|FailedStart)' -count=1
scripts/parity-gate.sh --case agent-session-stop-unblocks-start-state-notification
```

- [ ] **Step 5: Commit**

```bash
git add core/agent/agent_session.go core/agent/agent_session_stop_race_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(agent): unblock stop during state delivery"
```

### Task 2: Cancel an in-flight RoomIO text write

**Files:**
- Modify: `interface/worker/livekit/room_io.go`
- Modify: `interface/worker/livekit/room_io_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Consumes: `roomIOTextStreamWriter.Write(string)`, `roomIOTextStreamWriter.Close()`.
- Produces: `roomIOGuardedTextWriter` with independent `mu` and `writeMu` synchronization.

- [ ] **Step 1: Write the failing regression**

Change `gatedTextStream.Close` to close a cancellation channel that unblocks `Write`. Add `TestRoomIOCloseAgentTextStreamCancelsBlockedWrite`, wait for `writeEntered`, call `closeAgentTextStream`, and require both close and publish handshakes to return without manually releasing the write.

- [ ] **Step 2: Verify RED**

```bash
go test ./interface/worker/livekit -run '^TestRoomIOCloseAgentTextStreamCancelsBlockedWrite$' -count=1 -timeout 10s
```

Expected: timeout because `roomIOGuardedTextWriter.Close` waits for the mutex held by `Write` and never calls the inner cancellation method.

- [ ] **Step 3: Separate state and write synchronization**

Implement:

```go
type roomIOGuardedTextWriter struct {
    mu      sync.Mutex
    writeMu sync.Mutex
    inner   roomIOTextStreamWriter
    closed  bool
}
```

`Write` serializes through `writeMu`, checks `closed` under `mu`, then calls `inner.Write` without `mu`. `Close` sets `closed` under `mu` and calls `inner.Close` without `writeMu`.

- [ ] **Step 4: Verify GREEN and parity**

```bash
go test ./interface/worker/livekit -run 'TestRoomIO(CloseAgentTextStreamCancelsBlockedWrite|AgentTranscriptionWriteNeverLandsOnClosedStream)' -count=50
go test -race ./interface/worker/livekit -run 'TestRoomIO(CloseAgentTextStreamCancelsBlockedWrite|AgentTranscriptionWriteNeverLandsOnClosedStream)' -count=1
scripts/parity-gate.sh --case room-io-agent-transcription-close-cancels-blocked-write
```

- [ ] **Step 5: Commit**

```bash
git add interface/worker/livekit/room_io.go interface/worker/livekit/room_io_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "fix(worker): cancel blocked text stream writes"
```

### Task 3: Final validation

**Files:**
- Modify only if validation exposes a defect within Tasks 1-2.

**Interfaces:**
- Consumes: the two new parity cases and changed packages.
- Produces: validated clean branch state.

- [ ] **Step 1: Run focused packages**

```bash
go test ./core/agent ./interface/worker/livekit -count=1
```

- [ ] **Step 2: Run broad gates**

```bash
scripts/go-test-all.sh
scripts/go-build-all.sh
go-arch-lint check
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
```

- [ ] **Step 3: Inspect final state**

```bash
git diff --check origin/haikal/fix/memory-leak...HEAD
git status --short
git log --oneline origin/haikal/fix/memory-leak..HEAD
```
