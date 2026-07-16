# Review Deadlock Hardening Design

## Scope

Resolve two shutdown deadlocks identified during review:

1. `AgentSession.StartWithOptions` blocks while holding `lifecycleGate` when a subscribed state-event channel is full, preventing `Stop` from acquiring the gate and closing the run teardown signal.
2. `roomIOGuardedTextWriter.Write` holds its state mutex across `inner.Write`, preventing `Close` from reaching the underlying writer to cancel an in-flight blocking write.

No public APIs, channel capacities, or lossless-delivery guarantees change.

## Reference contracts

The source of truth is the vendored Python implementation:

- `livekit/agents/voice/agent_session.py`: `start` holds `_lock` through `_started = True` and `_update_agent_state("listening")`, with an explicit rule that no await follows the started transition. Go must preserve the same atomic state transition while ensuring its channel-based observer adapter cannot prevent teardown.
- `livekit/agents/voice/agent_session.py`: `_aclose_impl` acquires the same `_lock` before closing a started session. Go must not mutate teardown state before acquiring equivalent lifecycle ownership.
- `livekit/agents/voice/room_io/_output.py`: `capture_text` awaits writer delivery, while `flush` detaches the current writer and schedules `_flush_task`; `aclose` awaits pending flush and closes the remaining writer. Go must preserve serialized writer lifecycle and use close as the cancellation boundary for an in-flight SDK write.

## AgentSession lifecycle boundary

Startup remains serialized through validation, provider startup, assistant startup, activity installation, and the transition to a started run. After those state mutations are complete, `StartWithOptions` releases `lifecycleGate` before publishing `AgentStateListening` to subscribers.

The state notification retains its current lossless behavior. If it blocks, a concurrent `Stop` can acquire the lifecycle gate, signal the captured teardown generation, and release the notification producer. `Stop` does not signal teardown before acquiring the lifecycle gate, so a cancelled stop request cannot partially tear down a run it failed to own.

## RoomIO writer cancellation

`roomIOGuardedTextWriter` separates closed-state synchronization from write serialization:

- `mu` protects only `closed`.
- `writeMu` ensures at most one write enters the underlying writer at a time.
- `Write` acquires `writeMu`, rejects writes after closure under `mu`, then invokes `inner.Write` without holding `mu`.
- `Close` marks the writer closed under `mu` and immediately calls `inner.Close` without acquiring `writeMu`.

The underlying writer's `Close` is the cancellation mechanism for an active write. The SDK wrapper already bounds callback waiting with `roomIOTextStreamWriteTimeout`; test doubles must model cancellation by making `Close` unblock `Write`.

This guarantees that no new write begins after closure and that close can cancel an active write. It intentionally does not claim atomic ordering between a write already inside the underlying SDK and a concurrent close; that boundary is owned by the underlying writer cancellation contract.

## Testing

- Add a deterministic session test that fills an agent-state subscriber, starts the session until the listening notification blocks, invokes `Stop`, and requires both calls to return.
- Add a cancellation-aware blocked RoomIO writer test requiring `closeAgentTextStream` to return without an external write release.
- Preserve the existing no-new-write-after-close regression and adapt its fake to distinguish cancellation from successful post-close delivery.
- Run focused tests repeatedly and with `-race`, then run package, parity, build, architecture, staticcheck, and deadcode gates.

## Parity evidence

Both cases remain `go-test`. Python serializes session lifecycle with an asyncio lock but does not block on Go channel delivery, and Python awaits cancellable text-writer operations on one event loop. Scheduler deadlock behavior has no stable cross-runtime JSON trace.
