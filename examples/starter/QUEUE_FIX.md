# Helper queue overflow: fix and verification

The November15 cloud run joined successfully and sent 9.9s of native PCM to STT,
then ended with `native PCM consumer backlog exceeded eight frames`. The failure
was in the Go IPC bridge. The observability ZIP lacks per-frame IPC/consumer
measurements, so it cannot establish whether that incident was triggered by a
pipe burst, application blockage or host scheduling.

A deterministic regression reproduced a defect in that bridge: a buffered
40-frame burst was rejected as soon as the eight-frame channel filled, before
a consumer was allowed to drain it. A transient full channel was being treated
as unrecoverable overload. That does not establish a native receive-engine bug.

## Change

The queue still holds eight frames. Full-queue writes now wait for space or
cancellation. PCM order and bytes are preserved; no frame dropping, silence
padding, pacing, resampling or native receive policy was added. The default
transport branch is unchanged.

A callback taking more than two seconds cancels the experiment with
`native PCM consumer did not complete within two seconds`. This bounds callback
liveness, not packet arrival or media deadlines. The consumer contract requires
returning on cancellation; arbitrary Go callbacks cannot be forcibly unwound.
The existing helper interrupt/kill/reap lifecycle is retained.

Failure and shutdown logs now include an `ipc` object:

- `received_frames`: parsed, validated PCM messages, including a pending enqueue.
- `completed_callbacks`: consumer calls that returned, not proof of STT admission.
- `queue_waits` and `max_queue_wait_ms`: backpressure count and longest enqueue wait.
- `max_consume_ms`: longest completed callback, including cancellation return.

Backpressure does not solve sustained downstream under-capacity or prove bounded
native memory/latency under all host stalls. These diagnostics distinguish brief
queue saturation from slow callback processing in the next full workload run.

## Verification

| Check | Result |
| --- | --- |
| Old implementation, 40-frame burst | Failed immediately at eight queued frames; retained as regression evidence |
| Fixed implementation, 40-frame burst | All frames preserved in order |
| Real helper pipe, 600ms consumer pause | All 40 frames preserved; backpressure and callback delay observed |
| Cancellation while queue is full | Blocked enqueue returned cancellation |
| Stalled cooperative consumer | Explicit two-second stall error; child cleanup completed within test's six-second bound |
| Default/native/track-loss local smoke | Passed under race detector; native mode left Pion input unsubscribed |
| Two-minute real native receiver/session | 120.000s PCM in a 120.000s wall window; tone energy present; job remained active and shutdown completed |

The two-minute test uses the actual starter session, Go helper and native library
against a local LiveKit server. Its agent-input hook observes real decoded PCM
and returns before providers run. This is **readiness evidence for another load
test**, not a successful two-minute cloud/STT/LLM/TTS load test. The original
cloud scheduling cause and secondary shutdown warning are not claimed fixed.

The live window tolerance was fixed at 500ms before capture. Prior receive-engine
acceptance gates were neither changed nor rescored. Raw new logs are under
`examples/native-receive-probe/results/queue-backpressure/`. The incident ZIP,
`session_report.json`, `session/session.log` and all 2899 previously preserved
artifact hashes remain unchanged.

## Run and inspect

Offline regressions:

```sh
go test -race -count=1 ./examples/starter/... ./examples/local/...
```

The existing ignored `examples/local` copy in this workspace was updated too;
its `DeleteRoomOnClose` option and application configuration were preserved.
Only the starter implementation is tracked by Git. Rebuild/restart whichever
entrypoint you use. For the local entrypoint, with the existing opt-in/native
library and LiveKit environment:

```sh
go build -o examples/native-receive-probe/.cache/agent-local ./examples/local
examples/native-receive-probe/.cache/agent-local start
```

For the tracked starter, use [NATIVE_RECEIVE.md](NATIVE_RECEIVE.md). The helper
binary/protocol did not change, so rebuilding the agent is the necessary step.
Then run in another terminal configured for the same project:

```sh
lk perf agent-load-test --rooms 1 --agent-name "agent-starter-go" --duration 2m
```

Confirm the native-enabled/first-PCM messages and retain the final `ipc` metrics.
If another failure occurs, preserve that run rather than raising queue capacity
or weakening the native receive acceptance gates.
