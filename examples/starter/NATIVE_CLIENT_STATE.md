# In-process native client state

Implemented 2026-11-17 after caption checkpoint `fe88ff8f2`. Native FFI 0.12.80, rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`; tested macOS arm64. Default transport selection, Pion implementation, helper fallback, and native playout policies are unchanged.

## Contract and implementation

The in-process connection publishes the raw agent state as participant attribute `lk.agent.state` and reliable JSON messages on `lk-agent-state`:

- `{"type":"agent_state_changed","state":"listening"}` (and initializing/thinking/speaking).
- `{"type":"user_state_changed","state":"listening"}` (and speaking/away).

Mappings come directly from `agent.ClientAgentStateString` and `ClientUserStateString`, matching `interface/worker/livekit/client_events.go`. Idle maps to listening in data messages; the attribute retains the raw core state, matching default RoomIO. Unknown mapped states produce no corresponding data message. This is compatibility with the Go default transport, not an additional Python parity claim.

One observer subscribes before reading current state, publishes an initial snapshot after native readiness, and reads current state again on notifications. It unsubscribes on shutdown. A native runtime owner serializes acknowledged attribute, agent-data, and user-data operations. Rapid updates coalesce into the latest pending snapshot: this is current-state delivery, not a guarantee that every transient event appears remotely. The observer does not replay obsolete event values. Unchanged state data may be repeated as part of a complete snapshot.

Each session retains at most one in-flight state request, one active snapshot, and one latest pending snapshot. Payloads are bounded; PCM never waits for publication acknowledgements. The FFI data request copies an allocated C buffer before its async task starts (verified in pinned `livekit-ffi/src/server/room.rs::publish_data`); the C buffer is freed immediately after the request returns.

On native Reconnecting, new state publication pauses. On Reconnected, the latest snapshot is republished even without a fresh agent transition. An old in-flight operation must acknowledge before any newer operation is sent, preventing older attribute writes from overtaking newer ones. A missing acknowledgement emits a two-second timeout diagnostic but does not release that ordering barrier. Further snapshots remain coalesced until callback or session teardown; no unbounded retry queue or custom reconnect policy is added.

Failures are reported as native client-state publication diagnostics without failing PCM. An attribute failure does not suppress the independent data messages. The bounded error channel retains the latest diagnostic under a stalled observer. Shutdown cancels and joins the observer, stops unsent state stages, and retires pending callbacks at native session cleanup. Late callbacks cannot fail another session. An already-submitted request may complete during shutdown; there is no claim of retracting data already sent.

## Permissions

The assigned primary token must permit updating its own metadata/attributes (`canUpdateOwnMetadata`) and publishing data. No token is replaced, permission widened, or second participant created by this implementation. The existing generic local-job token lacks the attribute permission; the new integration test supplies its own explicit local-only token using the existing `LocalJobOptions.Token` fixture hook. Cloud dispatch permissions should be verified in the actual client test.

## Evidence

| Validation | Result |
|---|---|
| Default agent/user mappings, initial snapshot, transitions, cancellation/unsubscribe | Passed under race detector |
| Serialized/coalesced updates, reconnect refresh, failure isolation, pending bound/timeout, late callbacks and shutdown | Native-wrapper unit regressions passed under race detector |
| Full room, native adapter and starter package race suites | Passed |
| Default/helper example race suites | Passed |
| Tagged starter/local build, tagged vet, architecture | Passed |
| Local native room: attribute plus agent/user data received | Passed (`live-03`, final `live-04`) |

Final local run (`live-04`): two participants during operation, one after agent close; 5.000 s PCM / 5.000989 s wall; max PCM gap 28.020 ms; both output markers (50/51 frames); native session cleanup 4.052 ms, handles released, worker runtime Close succeeded. Existing PCM/content/cleanup acceptance gates were retained.

The local receiver verified initial listening, thinking with user speaking, agent speaking with user listening, and return to listening. State inputs were controlled core updates, not paid provider inference. Reconnect replay was tested with deterministic native lifecycle callbacks, not a new live signaling-outage run. Actual frontend rendering of client state and cloud-token permissions remain to be validated. Prior frontend caption confirmation remains separate evidence.

Preserved attempts: `live-01` failed because the fixture token could not update attributes; `live-02` failed compilation while specifying that permission as a bool rather than a pointer. Neither is relabeled passing. `live-03` and `live-04` passed. Raw logs and receiver/PCM summaries remain under `examples/native-receive-probe/results/native-client-state-20261117/`; aggregate results/hashes are in `NATIVE_CLIENT_STATE_RESULTS.json`.

## Try it

Use the same native mode and configuration as the successful captions build; no new runtime flag is needed:

```sh
go build -tags nativeinprocess -o agent-local ./examples/local
# Existing provider/model configuration and absolute LIVEKIT_LIB_PATH still apply.
RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1 \
RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM=0 \
RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE=0 ./agent-local start
```

The tracked starter also supports this behavior. Check listening/thinking/speaking indicators and caller state in the frontend, plus normal departure. Review `native client state` warnings if attributes fail while audio continues.

Offline local test, with the existing local server running and a new result directory:

```sh
LIVEKIT_LIB_PATH=/absolute/path/to/liblivekit_ffi.dylib \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/native-client-state-new" \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessNativeClientState$' -count=1 -timeout 90s -v
```
