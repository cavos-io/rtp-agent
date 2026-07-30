# SLNG Full Parity Design

## Objective

Reach behavioral parity with the vendored SLNG reference at
`refs/agents/livekit-plugins/livekit-plugins-slng`, commit
`48c179329522a44a979c97f58865773badf5241f`, while preserving existing Go
callers where compatibility does not contradict the current reference
contract.

Parity means matching observable endpoint construction, configuration,
headers, payloads, stream boundaries, event ordering, failure classification,
fallback selection, cancellation, and cleanup. Matching public names alone is
not parity.

## Scope

The work covers:

- shared SLNG model, endpoint, header, validation, and error helpers;
- STT construction, connection candidates, streaming lifecycle, fallback,
  reconnect, keepalive, finalization, events, errors, and shutdown;
- TTS construction, connection candidates, streaming lifecycle, fallback,
  chunking, warm standby, timeouts, events, errors, and shutdown;
- application configuration and provider composition;
- deterministic Go regression tests and executable cross-runtime parity cases
  where both implementations can run without network access.

Live provider integration testing and unrelated core runtime changes are out of
scope.

## Compatibility Policy

Current canonical Go APIs remain available:

- `NewSTT(apiKey string, opts ...STTOption) *STT`
- `NewTTS(apiKey string, opts ...TTSOption) *TTS`

New reference behavior is exposed through additional functional options and
provider-owned configuration types. Existing endpoint options remain supported
as deprecated compatibility paths. New default construction uses the current
Unmute Bridge endpoint shape:

```text
wss://api.slng.ai/v1/bridges/unmute/stt/<provider/model:variant>
wss://api.slng.ai/v1/bridges/unmute/tts/<provider/model:variant>
```

Compatibility paths may retain explicitly supplied legacy endpoints, but they
must not weaken validation for new model-based configuration.

## Architecture

All provider protocol behavior stays in `adapter/slng`. Core STT and TTS
interfaces remain provider-neutral. `app` only translates process
configuration into SLNG options and constructs adapters.

Shared connection and gateway rules move into focused provider files rather
than expanding `slng.go` indefinitely:

- `slng.go`: defaults and small shared coercion helpers;
- `connection.go`: connection configuration, Bridge endpoint/model parsing,
  candidate selection, and recovery cooldown;
- `gateway.go`: headers, tracking validation, provider error status mapping,
  and payload-independent gateway normalization;
- `stt.go`: STT API and stream lifecycle;
- `tts.go`: TTS API and stream lifecycle.

No new dependency is required. Existing Go standard library and Gorilla
WebSocket support the contract.

## Connection Contract

STT and TTS accept an ordered candidate list. A candidate contains:

- Bridge WebSocket endpoint;
- model derived from and validated against that endpoint;
- optional headers;
- optional init-payload overrides;
- optional TTS voice override.

Model strings and explicit candidates are mutually exclusive. Candidate
selection starts at the current active index. Failure advances to the next
candidate. Failure of the primary records a monotonic timestamp; after the
configured cooldown, a later stream retries the primary. A successful
candidate becomes active.

HTTP 413 terminates the whole chain. Non-retryable client errors do not advance
to another candidate. Retryable transport, timeout, server, rate-limit, and
backpressure failures may advance.

## Gateway Contract

Model identifiers use `provider/model` with an optional `:variant`. Bridge
endpoints must use `ws` or `wss`, contain a host, and match the current Unmute
Bridge service path.

Headers support:

- SLNG authorization;
- region override;
- world-part override;
- provider BYOK key;
- external agent ID;
- external session ID;
- caller-supplied extra headers;
- candidate-specific headers.

Tracking IDs are trimmed, limited to 128 characters, and reject commas and
control characters. Caller and candidate headers are applied before reserved
provider-key and tracking headers, so candidates cannot override credentials
or usage attribution.

Provider error frames map numeric and symbolic bridge codes to typed core API
errors. Error messages are bounded before inclusion in diagnostic metadata.

## STT Behavior

STT supports current reference streaming behavior:

- only 16-bit PCM input without transcoding;
- deterministic audio buffering aligned to sample boundaries;
- reference init payload and option update propagation;
- binary audio writes;
- periodic keepalive while input remains open;
- `finalize` when input ends;
- interim, final, speech-start, speech-end, and usage event ordering;
- active-stream option updates through reconnect;
- bounded graceful-close reconnects when no transcript was produced;
- candidate fallback and primary recovery cooldown;
- typed provider and connection errors;
- context cancellation and terminal provider shutdown;
- no goroutine or WebSocket leaks.

Legacy `flush` behavior remains only where an explicitly selected legacy
endpoint requires it.

## TTS Behavior

TTS supports current reference streaming behavior:

- reference init payload and provider-specific model normalization;
- word and phrase chunking modes, with `auto` selecting provider-safe behavior;
- Unicode-aware suppression of standalone non-letter tokens where required;
- streaming and chunked synthesis over the same lifecycle;
- first-audio timeout;
- candidate fallback and primary recovery cooldown;
- optional warm-standby connection reuse;
- runtime init overrides and per-candidate voice/header/init configuration;
- audio, completion, ignored control, malformed frame, and provider error
  handling;
- correct final boundaries after normal close;
- context cancellation and terminal provider shutdown;
- no goroutine or WebSocket leaks.

Warm standby is opt-in. Default behavior opens one WebSocket per stream.

## Application Wiring

`app` gains configuration translation for new SLNG options without leaking
SLNG types into core. Existing environment variables continue to work.
Additional fields are read only when explicitly configured, preserving current
defaults for callers outside SLNG.

Provider construction remains in the existing SLNG branches for primary and
fallback STT/TTS selection. Plugin registration remains unchanged.

## Error and Cancellation Rules

Every dial, read, write, reconnect, fallback, standby, and shutdown path
preserves `context.Canceled` and deadline errors when applicable. Provider
status errors use typed core API errors so retry and fallback policy can
classify them.

Each provider closes only streams and goroutines it created. `Close` is
idempotent. Closed stream iteration returns `io.EOF`; pushes after close return
`io.ErrClosedPipe`.

## Test Strategy

Implementation uses test-driven development. Each behavior begins with a
focused failing test, followed by the smallest production change.

Evidence levels:

1. Cross-runtime scenarios for pure model validation, Bridge endpoint parsing,
   header normalization, payload construction, error-code extraction, and
   candidate-state transitions.
2. Deterministic Go WebSocket tests for streaming order, reconnect, fallback,
   timeouts, cancellation, close/error behavior, partial/final output, and
   cleanup.
3. App tests for configuration translation and provider selection.

Default tests use only in-memory or loopback servers and require no provider
credentials or external network.

Validation includes:

```text
go test ./adapter/slng
go test ./app -run SLNG
go tool go-file-arch -config .architecture.yaml ./...
scripts/parity-gate.sh --case <changed-case>
scripts/parity-gate.sh
scripts/go-test-all.sh
scripts/go-build-all.sh
```

Broader commands run after focused tests pass. Staged-only guards are cited
only when their selected files are confirmed.

## Delivery Sequence

1. Shared Bridge connection and gateway contract.
2. STT current-reference lifecycle.
3. TTS current-reference lifecycle.
4. App configuration and compatibility migration.
5. Cross-runtime scenarios and full validation.

Each stage leaves the package compiling and its focused tests passing.

## Completion Criteria

Full parity is claimed only when:

- every applicable observable reference behavior has executable evidence;
- remaining differences are explicitly classified as intentional Go behavior,
  inapplicable framework behavior, or reference-version conflict;
- all focused and repository parity gates pass;
- architecture checks pass;
- no paid or external provider access is required;
- compatibility behavior and deprecations are documented in code;
- no unclassified drift remains.
