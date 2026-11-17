# Native DTMF and basic call control

Opt-in in-process implementation, 2026-11-17. Default Pion selection and helper fallback are unchanged. Native FFI 0.12.80 / rust-sdks `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`; tested on macOS arm64 with Go race instrumentation (not Rust/C++ instrumentation).

## Supported scope

- Incoming native SIP DTMF packets reach `AgentSession.SubscribeSipDTMFEvents()` with code, digit and sender identity. Repeated digits remain separate events. All senders are forwarded, matching the default adapter.
- `nativeroom.Room.PublishDTMF` implements the existing send-DTMF tool contract. Codes 0–15 correspond to `0123456789*#ABCD`; mismatched code/digit pairs are rejected. Native publication acknowledgement is not proof a telephone/IVR processed the digit.
- `Room.Shutdown` ends its job. `Room.DeleteRoom` uses the server API to disconnect everyone in that room. It marks intentional shutdown before deletion, bounds the request to three seconds and logs/returns API failures. Fake jobs cannot delete rooms through this method.
- `Room.CallTools()` returns session-bound `send_dtmf_events` and `end_call` tools. The existing end-call tool waits for playout when invoked with a RunContext, deletes the room, then shuts down. Its existing asynchronous result is not a deletion acknowledgement; deletion failures remain logged by the native adapter.

This adds no outbound dialing, SIP trunk provisioning, transfer, or hold workflow. Real PSTN/trunk interoperability remains untested. The local proof uses LiveKit SIP DTMF data packets between two room participants, not a telephone call.

## Application use

Native DTMF reception is enabled with the existing in-process transport. Subscribe before expecting digits, and invoke the returned unsubscribe function on cleanup. The legacy unclaimed inbox remains best-effort, matching the default transport; this is not durable event storage.

Call tools require explicit application registration. Where the application constructs its native transport, before `job.StartSession`:

```go
transport := nativeroom.NewInProcess(job, session, cfg, apiKey, apiSecret)
session.Tools = append(session.Tools, transport.CallTools()...)
return job.StartSession(ctx, session, worker.StartSessionOptions{Transport: transport})
```

Register only once, and avoid duplicate tool IDs. This changes the session's tool list; do not put room-bound tools on a shared base Agent. The stock starter/local selector does not automatically expose end-call to the LLM. Direct integrations can instead call `PublishDTMFContext`, `DeleteRoom`, or `Shutdown` on the native room returned by `job.PrimaryRoomIO()`.

## Delivery and lifecycle

Incoming native delivery has a 32-event queue. A full queue fails only the affected session; it never silently coalesces keypad digits. Forwarding to a claimed core subscriber allows two seconds before failing that session. Cancellation joins the forwarding goroutine even when the consumer is stalled. Existing default `EmitSipDTMF` is unchanged.

Only one outgoing DTMF publication is in flight per session. Startup/reconnect/closed sessions reject sends; uncertain sends are never replayed. A cancellation after submission reports uncertain delivery and retains the slot until the callback or teardown. A caller must not automatically retry such a digit. Late callbacks after teardown are ignored safely. These are data-message delivery bounds, not audio playout policies.

## Validation

Artifacts: `examples/native-receive-probe/results/native-dtmf-20261117/`; hashes in [NATIVE_DTMF_RESULTS.json](NATIVE_DTMF_RESULTS.json).

Local race-enabled run `live-03` passed:

| Measurement | Result |
|---|---:|
| Incoming sequence / sender | `1,1,#` / `caller-test` |
| Outgoing sequence / sender | `2,2,*` / `primary-agent` |
| Participants during / after end-call | 2 / 0 |
| Steady PCM / wall | 5.000 / 5.000909 s |
| Maximum input callback gap | 22.027 ms |
| Output marker frames, 990 / 1320 Hz | 51 / 51 |
| Native cleanup | 4.935 ms; handles released, disconnect callback seen |
| Entire job cleanup | 2020.326 ms |

Existing harness PCM, output-marker and cleanup gates were retained. Room absence was checked with ListRooms. This is a short feature test, not another soak or a cloud telephony validation.

Preserved attempts:

- `live-01`: bidirectional digits passed; deletion assertion failed because the fixture used a fake job, whose Job.DeleteRoom intentionally skips deletion.
- `live-02`: switching to a real local job omitted mandatory RoomInfo; startup rejected it. Harness now supplies RoomInfo and reports early execution errors immediately.
- `native-regression.log`: new dispatch fixture omitted required protobuf packet kind; corrected in `native-regression-02.log`.
- `regression.log`: adapter, starter and beta-tool suites passed, but the broad core-agent race suite reported failures in existing speech-done/false-interruption, commit-turn and pipeline tests. No DTMF frame appears in those race stacks. Kept for separate investigation; not claimed as a passing full suite or proven pre-existing on a clean baseline.

Reproduce against the existing local test server configuration (no paid providers):

```sh
livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml
# In another terminal; choose a new, nonexistent output directory every run:
LIVEKIT_LIB_PATH=/absolute/path/liblivekit_ffi.dylib \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR=/tmp/native-dtmf-new-run \
go test -race -tags 'nativeinprocess nativeroomintegration' ./examples/starter \
  -run '^TestStarterInProcessNativeDTMF$' -count=1 -timeout 90s -v
```

Regression checks cover native event routing, repeated digits, overflow isolation, invalid/canceled sends, uncertain-delivery slot retention, reconnect rejection, late callbacks, stalled consumers, deletion failure visibility and independent session tool instances.
