# Native agent.events live local validation — 2026-11-18

Result: **PASS**, race-enabled executable, exit 0, no race reports (2.34s).
A real loopback LiveKit server and the pinned native FFI carried chat and evidence.
The worker used the downstream native transport factory, TestEvidencePublisher,
NewAgentTestTextInputCallback and the upstream caption forwarding chain. A separate
LiveKit observer received the packets over the room data channel.

## Verified

- `status/ready_for_tester` arrives from `native-agent` with the expected run ID.
- Two `lk.chat` text-stream inputs from the configured tester identity traverse
  native input and the downstream callback, each producing an acknowledgement
  and completed agent transcript with matching run_id, message_id and turn.
- Duplicate turn 2 produces another acknowledgement and the cached transcript,
  while the synthetic assistant's generation count remains exactly two.
- All seven evidence packets have the expected agent sender identity; no extra
  packet arrived during the subsequent 500ms observation window.
- Job shutdown and native runtime close returned without a test failure.

`events.jsonl` contains the received payloads and sender identities. `result.log`
is the test-result excerpt; `manifest.json` identifies the raw log and its hash.

## Scope and limits

This validates real native transport delivery and downstream evidence components.
The synthetic assistant emits deterministic final text through the real session
caption/evidence forwarding chain; it does not call an LLM or TTS provider.
The fixture manually wires the same input callback and next-output publisher,
bypassing app.Run, dispatch snapshots and the manager/UI automated-test workflow.
It does not certify provider quality, packet-loss/reconnect behavior, destination
filtering, all evidence event types, Linux execution or cross-room isolation.
The Pion SDK is used by the tester/observer clients; the agent uses native FFI.

Transcription output was enabled, as required by the current downstream
TranscriptionNextOutput evidence chain. The runtime publishes this topic using
its reliable data path; the observer's SDK callback does not expose reliability
kind, so the test asserts received payloads rather than inspecting wire framing.

No real credentials or STG calls were used. The fixture was added with Go overlay;
no downstream source files were edited or committed. Pion remains default.

## Reproduction

Use the loopback LiveKit configuration and synthetic key pair described in
[the earlier downstream local fixture](../native-downstream-local-20261118/README.md).
Map that report's `local_native_acceptance_test.go.txt` to a temporary downstream
`app/local_native_acceptance_test.go`, and this report's `evidence_test.go.txt` to
`app/local_native_evidence_test.go` with Go overlay. The former supplies the caller
publisher helper; its drain test is not selected here.

From agent-worker-golang:

```sh
go test -overlay /path/to/overlay.json -race -tags 'nativeinprocess nativeacceptance' ./app -run '^TestLocalNativeEvidenceDelivery$' -c -o /tmp/evidence.test
```

Run the binary from a temporary directory (avoiding dotenv) with
`NATIVE_ACCEPTANCE_URL=ws://127.0.0.1:17880`, `LIVEKIT_LIB_PATH` pointing to the
pinned dylib, and `NATIVE_ACCEPTANCE_OUT` pointing to a new output directory:

```sh
/tmp/evidence.test -test.run '^TestLocalNativeEvidenceDelivery$' -test.timeout=70s -test.v
```

Original fixture, overlay, executable and logs remain under
`/private/tmp/native-evidence-20261118/`. The temporary LiveKit server was stopped
after validation.
