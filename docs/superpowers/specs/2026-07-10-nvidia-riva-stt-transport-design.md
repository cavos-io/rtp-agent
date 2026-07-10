# NVIDIA Riva Streaming STT Transport Design

## Objective

Replace the NVIDIA adapter's placeholder streaming STT failure with a real
Riva bidirectional gRPC transport. Preserve the LiveKit NVIDIA plugin's
observable behavior for live calls: audio ordering, interim/final transcript
ordering, endpoint flush, diarization, cancellation, and authentication.

This slice covers streaming STT only. Offline recognition remains unsupported,
matching `STT._recognize_impl` in the Python reference. NVIDIA TTS and
PersonaPlex realtime behavior are outside this slice.

## Why This Gap Matters

`NvidiaSTT.Stream` currently accepts microphone audio but reports
`nvidia riva stt streaming is not implemented` from the output path. No live
NVIDIA call can receive transcripts. Transport completion has higher voice-call
value than further metadata or symbol parity work.

## Behavioral Reference

Primary Python files:

- `refs/agents/livekit-plugins/livekit-plugins-nvidia/livekit/plugins/nvidia/stt.py`
- `refs/agents/livekit-plugins/livekit-plugins-nvidia/livekit/plugins/nvidia/auth.py`
- `refs/agents/livekit-agents/livekit/agents/stt/stt.py`

Protocol source:

- NVIDIA `nvidia-riva/common`, main commit
  `71df98266725320a6b6b3a9f32a6da832dc93691`
- Required files: `riva/proto/riva_audio.proto`,
  `riva/proto/riva_common.proto`, and `riva/proto/riva_asr.proto`

Generated Go bindings must record this upstream repository and commit in a
package comment or adjacent provenance file. Generated bindings are read-only
outputs; changes require regeneration from the pinned source. Preserve all
license notices required by the upstream NVIDIA Riva common repository.

## Target Packages and Files

Production changes stay inside `adapter/nvidia/**`:

- `adapter/nvidia/stt.go`: provider options and public stream surface
- `adapter/nvidia/stt_transport.go`: gRPC connection, send/receive lifecycle,
  metadata, and response conversion
- `adapter/nvidia/internal/rivapb/*.pb.go`: generated NVIDIA protocol bindings
- `adapter/nvidia/nvidia_test.go`: focused transport and lifecycle tests

The only planned non-adapter behavior change is a manifest row in
`scripts/parity-fixtures/test-cases.tsv`. `go.mod` and `go.sum` may change only
if generation or transport needs a dependency not already present. The current
module already includes gRPC and protobuf runtimes.

## Chosen Approach

Use typed Go bindings generated from official NVIDIA Riva protocol files.
Keep them under the NVIDIA adapter so provider protocol types cannot leak into
`core`.

Rejected alternatives:

- Dynamic protobuf descriptors: smaller source footprint, but opaque and
  brittle for streaming oneofs and response conversion.
- An unofficial Riva Go client: smaller local code, but adds ownership,
  compatibility, and supply-chain risk without removing protocol coupling.

## Architecture

`NvidiaSTT` remains the provider configuration object. It creates a
`nvidiaSTTStream` with a transport owned by that stream. A narrow internal
client boundary permits an in-process fake gRPC service in tests without
changing the exported adapter API.

The transport has three responsibilities:

1. Build the Riva connection and outgoing metadata.
2. Send one streaming config followed by normalized PCM audio in input order.
3. Receive Riva responses and enqueue existing `stt.SpeechEvent` values in
   provider order.

The existing transcript conversion helpers remain authoritative for event
types, request IDs, timing offsets, word timings, and speaker-majority logic.
The transport must not duplicate that behavior.

## Connection and Authentication

Hosted mode (`use_ssl=true`):

- Use TLS transport credentials.
- Attach an `authorization` value formed as `Bearer ` plus the configured API
  key when that key is non-empty.
- Attach the configured `function-id` value on every stream.

Local mode (`use_ssl=false`):

- Use insecure gRPC transport credentials.
- Do not require or synthesize an authorization header.
- Attach `function-id` exactly as configured, including an empty value.

The streaming RPC must use a caller-derived context, and closing the stream must
close its client connection. No cloud credentials or external network access
are required by default tests.

## Streaming Configuration Contract

The first client message must contain `StreamingRecognitionConfig` with:

- encoding: `LINEAR_PCM`
- language code: stream-effective language
- model: configured model
- maximum alternatives: `1`
- automatic punctuation: configured `punctuate`
- sample rate: configured sample rate
- audio channel count: `1`
- word time offsets: enabled
- interim results: enabled
- diarization config only when enabled, including configured maximum speaker
  count

Every later client message contains only `audio_content`. Audio bytes are the
mono, configured-rate PCM produced by existing normalization code. Empty
frames are not sent. Frame order is preserved.

## Lifecycle and Data Flow

1. `Stream` creates the transport-owned context and starts one worker.
2. The worker opens `StreamingRecognize`, sends configuration, then services
   audio send and response receive until flush, cancellation, close, or error.
3. `PushFrame` performs existing sample-rate checks and normalization, then
   queues non-empty PCM without waiting for a provider response.
4. Received responses are processed in response order and result order. Blank
   transcripts are skipped. Generated events retain existing reference order:
   `start_of_speech`, interim or final transcript, then `end_of_speech` after a
   final result.
5. `Flush` drains buffered resampler output, queues it before the endpoint,
   closes the gRPC send side, and allows receive draining through provider EOF.
6. `EndInput` performs the same flush-before-close sequence and prevents later
   input.
7. `Close` and caller cancellation cancel transport work, unblock `Next`, close
   the connection, and wait for worker exit. No goroutine or gRPC connection
   may survive stream closure.

The Python NVIDIA worker stops collecting input on its first flush sentinel.
Therefore later frames may still pass the base stream's input validation but
must not be sent to the completed provider stream.

## Error Contract

- Configuration/open/send/receive failures become stream output errors from
  `Next`; `PushFrame` remains an input-queue operation unless input is closed
  or its caller context is canceled.
- Caller cancellation wins over provider errors when cancellation is already
  observable.
- Provider EOF after a clean flush becomes `io.EOF` only after queued transcript
  events are drained.
- `Close` is idempotent.
- Late `PushFrame`, `Flush`, or `EndInput` after closed input keep the existing
  `stream input ended` contract.

The placeholder `nvidia riva stt streaming is not implemented` error is removed
from the streaming path. Offline `Recognize` keeps `Not implemented`.

## Concurrency Invariants

- One stream owns one connection and one Riva streaming RPC.
- Configuration precedes all audio.
- Audio and transcript event order never depend on map iteration.
- State transitions and event queues are synchronized without holding the
  stream mutex across blocking gRPC calls.
- Worker completion wakes every blocked `Next` call.
- Final events already received are delivered before terminal EOF/error.

## Test Design

Use Go tests in `./adapter/nvidia` with an in-process gRPC server implementing
the generated Riva ASR service.

Focused tests must prove:

- local transport sends exact configuration followed by PCM frames in order
- hosted transport chooses TLS and supplies authorization/function metadata
  through a testable dial/credential boundary without external network calls
- local transport omits authorization and retains function metadata
- interim and final provider responses preserve event order and transcript data
- blank alternatives are skipped without reordering later results
- flush sends buffered resampler tail before closing the send side
- provider final responses after client close-send are drained before EOF
- context cancellation and `Close` unblock send, receive, and `Next`
- transport errors surface once and do not leak goroutines
- diarization fields and final speaker attribution match the reference

Tests use explicit channels/deadlines, not sleeps, for transport sequencing.

## Parity Evidence

Selected layer: manifest-linked `go-test`.

Planned manifest case:

- `nvidia-stt-reference-native-streaming-transport`

Contract summary:

> NVIDIA STT sends reference Riva streaming configuration and ordered mono PCM,
> then emits ordered interim/final speech events while draining final responses
> across endpoint close.

A cross-runtime case is not selected because the Python Riva client requires a
provider service and its threaded runtime is not deterministic enough for the
existing generic runners. The in-process Go gRPC contract test directly encodes
the inspected Python request, lifecycle, and response behavior.

Python reference evidence command:

```sh
uv run pytest refs/agents/livekit-plugins/livekit-plugins-nvidia
```

This command is reference evidence only, is unverified for this design, and may
require the vendored reference environment. It is not required for the default
Go test.

Planned target validation (not executable until implementation and the manifest
row exist):

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportMatchesReference$' -count=1
scripts/parity-validate.sh --case nvidia-stt-reference-native-streaming-transport
go test ./adapter/nvidia -count=1
scripts/parity-gate.sh --case nvidia-stt-reference-native-streaming-transport
```

The targeted parity gate is justified because this slice adds generated
protocol code, concurrency, and a real provider boundary. Full parity gate is
not required unless shared files or dependencies change beyond the manifest and
existing gRPC/protobuf modules.

## Delivery Boundaries

- Do not edit `refs/agents/*`.
- Do not add per-scenario JSON files or one-off runners.
- Do not expose Riva protobuf types through `core/stt`.
- Do not implement offline recognition, TTS transport, retry policy, or stream
  reconnection in this slice.
- Do not weaken existing unsupported/lifecycle tests; replace only assertions
  made obsolete by the now-supported streaming transport.
- Generated protocol code and transport must be exercised by real flow and
  focused tests; no inert client interfaces or fake call sites.

## Completion Criteria

The slice is complete only when:

1. Local and hosted connection configuration are covered without external
   network calls.
2. A fake Riva server receives config and ordered normalized audio.
3. Interim/final events and endpoint draining match the reference contract.
4. Cancellation and close leave no blocked worker or connection.
5. Focused tests, targeted parity validation, targeted parity gate, build,
   architecture, and staged dead-code checks pass.
6. The micro-commit is rebased onto latest `main` and revalidated.
