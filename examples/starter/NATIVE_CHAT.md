# In-process native chat

In-process sessions now receive completed `lk.chat` text streams from the selected caller. `RoomOptions.TextInputCallback` receives the text, participant identity and protobuf metadata. Without a callback, the transport reuses the default interrupt/claim-user-turn/generate-reply handling. `DisableTextInput` disables native chat reading. Helper fallback and default transport selection are unchanged.

## Callback metadata migration

`livekit.TextInputEvent.Info` is now `*ffipb.TextStreamInfo`, imported from `github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb`. This is an intentional source API change. Use `Info.GetStreamId()` instead of `Info.Id`, `Info.GetTotalLength()` instead of dereferencing `Info.Size`, and protobuf getters for other fields. `TotalLength == nil` still distinguishes an absent length from zero. The default Pion path converts SDK metadata into this same protobuf type, cloning attributes and optional length; callback routing and default transport behavior stay unchanged. The Go SDK does not expose operation/encryption metadata: those required fields use protocol defaults (CREATE/NONE); optional native-only fields remain unset. Native callback metadata retains its actual FFI fields.

All 14 pinned generated FFI source files moved unchanged from the probe to the worker LiveKit package, together with upstream license, notice and provenance. No duplicate protobuf registration, SDK fork, unsafe metadata construction or generated-source edits. The optional maintainer generator targets the new directory; builds still require no generation step.

## Delivery and lifecycle

Each session has one callback consumer, at most eight completed messages queued, at most eight active readers and a 64 KiB per-stream payload limit. Native receives the same maximum decompressed payload bound. Partial text is never dispatched. A 30-second incomplete-stream timeout, read error or queue/reader overflow fails only the affected session visibly; these bounds are additional experimental limits relative to Pion. Callback errors and panics are logged and do not stop subsequent chat delivery.

Shutdown cancels callback context and waits within the close context. Callbacks must cooperate with cancellation; a callback that ignores context cannot be forcibly stopped, and close returns its timeout. Queued messages are not delivered after cancellation is observed.

The pinned Rust FFI consumes the reader handle in `TextReadIncremental` (`take_handle`) and owns the asynchronous read thereafter. Go retains only correlation/text state and timers, and must not drop that consumed handle again. Ignored streams are released by normal dispatch cleanup. Native room shutdown owns native reader termination; Go removes correlation state and ignores late events. No custom chunk transport, audio playout or reconnect policy was added.

## Evidence

Results: `../native-receive-probe/results/native-chat-20261117/`.

- Race-enabled local starter test: `hello 世界`, stream ID, topic, attributes and selected-caller identity delivered through the real callback in a two-participant room. Five seconds PCM, maximum input callback gap 23.321 ms, native handles released on shutdown.
- Regression tests cover completed/partial delivery, metadata, filtering, disabled/canceled sessions, queue and reader bounds, session isolation, handle ownership, late events, callback errors/panics, cooperative cancellation and noncooperative shutdown timeout.
- Existing helper/probe suites and default worker/room suites remain part of verification. Initial sandbox-denied listener attempts are preserved separately from successful reruns.
- Default Pion chat metadata was also checked over a real local room under the Go race detector, including protobuf serialization. Missing required metadata fields found during validation were fixed; the failed run is retained.
- Repository suite (`scripts/go-test-all.sh`), focused race suites, architecture validation and vet passed. See [result manifest](NATIVE_CHAT_RESULTS.json).
- Prior artifact verification: 3,630 hashes matched; all 14 moved generated files are byte-identical to their checkpoint versions.

Cloud/browser chat validation remains outstanding. These tests do not certify native as the default transport or remove previously documented compatibility gaps.
