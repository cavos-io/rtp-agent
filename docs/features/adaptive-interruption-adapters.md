# Implementing adaptive interruption adapters

This guide is for coding agents and maintainers adding an acoustic interruption
detector to RTP Agent. Adaptive interruption classifies speech overlapping audible
agent speech as a barge-in or a backchannel. Core uses that verdict to interrupt
playback or discard backchannel transcripts. End-of-turn detection is a separate
capability.

Read [ARCHITECTURE.md](../../ARCHITECTURE.md) and
[DEVELOPMENT.md](../../DEVELOPMENT.md) before implementation. The
[repository skill](../../.agents/skills/rtp-agent-adaptive-interruption/SKILL.md)
routes agents here. The [implementation prompt](#implementation-prompt) supplies a
copyable task brief.

## Choose the implementation boundary

| Layer | Responsibility |
| --- | --- |
| `core/agent` | Detector contracts, speech/overlap boundaries, cooldowns, playback interruption, transcript holding/filtering/release, endpointing, telemetry, and VAD fallback |
| `adapter/<provider>` | Model inputs, audio conversion, authentication, wire protocol, inference scheduling, response correlation, provider errors, and stream resources |
| `app` or an example consumer | Concrete provider selection and session options |
| `interface/worker` | Room transport and media delivery |

Implement the existing core interfaces in the provider package. Use
`adaptive_interruption.go` and `adaptive_interruption_test.go`, following the
LiveKit constructor/option pattern where appropriate. Add compile-time assertions
for both interfaces. Keep provider-neutral interfaces in core.

The current [architecture guard](../../.architecture.yaml) requires a provider
directory to have `plugin.go` metadata and at least one recognized capability file
such as `stt.go` or `tts.go`. **`adaptive_interruption.go` alone does not satisfy
that rule.** Adding a detector to an existing provider package fits the current
contract. A new standalone detector package needs an explicitly scoped update to
both the architecture document and guard. Do not add a dummy capability to pass
the guard.

## Current detector contract

The definitions in [adaptive_interruption.go](../../core/agent/adaptive_interruption.go)
are authoritative:

```go
type InterruptionDetector interface {
	Stream(context.Context) (InterruptionStream, error)
}

type InterruptionStream interface {
	PushFrame(*model.AudioFrame) error
	StartAgentSpeech() error
	EndAgentSpeech(time.Time) error
	StartOverlap(time.Time) error
	EndOverlap(time.Time, bool) error
	Next() (OverlappingSpeechEvent, error)
	Close() error
}
```

Here `model` is `core/audio/model`. `Stream` creates a pipeline-owned stream using
the session run context. Pipeline shutdown closes it. Core pushes processed user
audio after the audio hook, turn-stream input, and VAD input, before STT input.
Frames arrive outside overlaps too; the adapter decides which frames to retain
and when to infer. Core sends the speech and overlap boundaries separately.

| Method | Adapter behavior |
| --- | --- |
| `PushFrame` | Accept user PCM frames; validate/convert format and retain only the model's required history. Keep transport callbacks responsive. |
| `StartAgentSpeech` | Start a new audible agent interval; reset prior interval state and invalidate old requests. |
| `StartOverlap(startedAt)` | Bind inference to this exact overlap timestamp. Define repeated-start behavior without duplicating work. |
| `EndOverlap(endedAt, agentEnded)` | Resolve or cancel pending inference according to the model policy. Produce one terminal event for an unresolved overlap, or return an error; avoid another event for an overlap already resolved. `agentEnded=true` means the agent stopped before a verdict. |
| `EndAgentSpeech(endedAt)` | End the agent interval, clear its history, and resolve any pending overlap as agent-ended. |
| `Next` | Wait for a terminal verdict or error; unblock on cancellation or close. |
| `Close` | Release owned connections, buffers, workers, and timers; make repeated close safe. |

Audio input, boundary calls, result consumption, and close can run concurrently.
Protect shared state and test their ordering. Bound network operations and define
a queue/backpressure policy for audio bursts and slow consumers. Avoid holding a
state lock during network I/O. Cancellation must stop every owned worker.

### Transcript-based detectors

The current interface has **no transcript input method**. Untimed STT support
does not make the detector transcript-aware. A provider requiring interim/final
text needs a separately scoped core capability extension and tests for delivery
before core holds STT events, late finals, cancellation, and overlap correlation.
Do not call or invent `PushTranscript` as an existing API, or bypass core's
transcript lifecycle in the adapter.

## Verdicts, correlation, and failures

Return [OverlappingSpeechEvent](../../core/agent/events.go) through `Next`:

| Field | Meaning |
| --- | --- |
| `OverlapStartedAt` | Preserve the exact timestamp passed to `StartOverlap`. Core compares `UnixNano()` with the pending overlap; a missing, rounded, or stale timestamp cannot interrupt current playback. |
| `IsInterruption` | `true` for a confirmed barge-in; `false` for a completed backchannel classification when `AgentEnded` is false. |
| `AgentEnded` | Agent speech stopped before classification. This is inconclusive and must not count as a backchannel. |
| `CreatedAt`, `DetectedAt` | Event creation time and verdict/boundary time. Keep wire request IDs separate from these event timestamps. |
| `TotalDuration`, `PredictionDuration`, `DetectionDelay` | Go `time.Duration` values. Core converts them to seconds for metrics and JSON. State the measurement anchors supported by the provider. |
| `Probability`, `Probabilities`, `SpeechInput` | Optional model diagnostics. Supply meaningful values when available; do not fabricate confidence. |
| `NumRequests` | Usage attributable to this overlap. Define what is counted and ensure a terminal event accounts for it once. |

Correlate asynchronous replies to the active overlap and agent interval inside the
adapter. Ignore unknown, duplicate, or stale replies, including replies after
overlap end, agent speech end, and a later `StartAgentSpeech`. Core's correlation
check is an additional safeguard. Core owns the actual interruption and transcript
decisions; adapters emit verdicts instead of invoking playback or turn APIs.

Startup failures or a nil stream leave core using VAD. During operation, errors
from audio input or `Next` trigger core fallback, which releases held transcripts
and restores VAD handling. Report timeouts, malformed responses, quota failures,
and unexpected disconnects as errors, preserving the cause. Do not turn an
inference failure into a negative verdict. In the current consumer, `io.EOF`
terminates result consumption without activating fallback; an unexpected provider
disconnect therefore needs a non-EOF error.

### Request accounting and traces

Core emits `InterruptionMetrics` for each returned event, including events whose
activity has already moved on. If the detector implements both `Model() string`
and `Provider() string`, core uses them as metric metadata.

The LiveKit adapter increments `NumRequests` when it **enqueues** an inference
request. `lk.agents.usage.interruption_num_requests` consequently measures queued
requests, rather than confirmed inference or billing. Do not compare a call-wide
sum directly with a provider's per-minute quota. Verify the provider's quota unit,
time window, and scope, and distinguish enqueued, sent, acknowledged, completed,
and billed requests when diagnosing usage.

An interrupted agent transcript records a playback outcome. `user_speaking`
classification and `lk.interruption.*` details depend on core accepting the
corresponding verdict and having an eligible span. These signals need not have
identical counts. Preserve core telemetry ownership rather than adding parallel
span mutation in the adapter.

## LiveKit as a worked reference

Read [the adapter](../../adapter/livekit/adaptive_interruption.go) and its
[offline tests](../../adapter/livekit/adaptive_interruption_test.go) together.
Its current provider-specific choices are:

- A WebSocket connection to `/bargein`, authenticated with an inference access
  token; `session.create` selects 16 kHz mono `s16le` and `min_frames: 2`.
- Resampling/downmixing inside the adapter. Audio is buffered while the agent
  speaks; inference is queued only during an active overlap.
- A rolling three-second history, a first-overlap prefix of one second plus
  already-observed VAD speech, and a 1,600-sample (100 ms) inference cadence.
- Binary requests carrying an eight-byte request ID followed by PCM. Positive
  replies are matched against pending IDs; `inference_done` removes the matching
  pending request. Overlap end without a positive verdict emits a negative
  terminal event, while agent end emits an inconclusive terminal event.
- A 700 ms pending-request timeout, 700 ms write deadline, and 100 ms timeout
  polling. Its request list has no explicit queue-size bound; do not infer a
  bounded queue from the timeout policy.

These values and protocol details are implementation examples, not requirements
for another model. Establish each provider's audio format, history/window,
cadence, threshold, deadlines, quota, and overlap-end policy from its authoritative
documentation or implementation, recording the revision used. An elapsed audio
window can include silence; it does not promise that duration of voiced speech.

## Compose and enable a detector

This helper uses the existing LiveKit implementation and returns options for a
consumer's normal session construction:

```go
package adaptiveexample

import (
	"github.com/cavos-io/rtp-agent/adapter/livekit"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func AdaptiveOptions(apiKey, apiSecret string) agent.AgentSessionOptions {
	return agent.AgentSessionOptions{
		InterruptionMode: agent.InterruptionModeAdaptive,
		InterruptionDetector: livekit.NewAdaptiveInterruptionDetector(
			apiKey, apiSecret,
		),
	}
}
```

Pass the selected detector through `AgentSessionOptions.InterruptionDetector` in
the intended composition root. Preserve the consumer's other session options.
Adaptive mode requires an STT pipeline, VAD, STT, enabled interruptions, and STT
audio while the agent speaks (`HalfDuplexSTTWhileSpeaking` must be false).
Manual and realtime-LLM turn detection are rejected. Aligned STT timestamps are
optional: core supports timed and untimed transcripts. Empty or `vad` interruption
mode retains VAD behavior. See [session startup](../../core/agent/agent_session.go)
for the effective validation rules.

## Implementation and verification checklist

1. Record the target package, authoritative model/protocol source and revision,
   model inputs, inference policy, failure policy, and request-count definition.
   Check whether the existing audio-only contract can express the provider.
2. Implement the two interfaces in the provider adapter, with format conversion,
   correlation, cancellation, and owned resource cleanup. Keep core's speech,
   transcript, endpointing, and telemetry decisions intact.
3. Add deterministic offline tests using local fake transport or model responses.
   Cover format conversion and malformed input; agent/overlap boundaries; no
   inference outside eligible overlaps; barge-in, backchannel, and agent-ended
   outcomes; repeated boundaries; stale/duplicate replies; request accounting;
   startup/protocol/timeout errors; burst input; cancellation; blocked `Next`;
   and repeated close. Use synchronization instead of timing-dependent sleeps.
4. Connect the detector to the requested composition root. Reuse core regression
   tests for cooldowns, transcript release/filtering, fallback, handoff, and span
   lifecycle. Add core tests only for a demonstrated core behavior change.
5. Run focused tests and race checks for core and the target adapter, then the
   architecture guard. For explicit reference parity, follow
   [the parity workflow](../../.agents/skills/rtp-agent-parity/SKILL.md), record
   reference evidence, and run the full parity gate required by DEVELOPMENT.
   Broaden tests/builds when changes cross packages.

For the existing LiveKit adapter, the focused commands are:

```sh
nix develop -c go test ./core/agent ./adapter/livekit -run 'TestAdaptive' -count=1
nix develop -c go test -race ./core/agent ./adapter/livekit -run 'TestAdaptive' -count=1
nix develop -c go tool go-file-arch -config .architecture.yaml ./...
```

Replace `./adapter/livekit` with the target package and include any new tests with
different names. Use the broader commands in DEVELOPMENT when their scope
applies. Report commands actually run, evidence limits, and any remaining
unsupported provider capability.

## Diagnose an adapter in a session

First confirm that the agent enters the speaking state during audible playback,
the detector stream starts, processed input reaches it, and an eligible overlap
starts. Check cooldown ownership and fallback errors before treating a missing
positive span as a model failure.

If speech overlap sounds silent in a recording, inspect the recording tap,
timestamp/pacing semantics, TTS chunk sizes, and playback-state transitions.
Recorded silence alone does not establish silence at VAD, detector, or STT input.
Recheck the current TTS and recorder paths before assigning the problem to Azure
or another provider; whole-response buffering and playback timing can affect
multiple TTS implementations and VAD mode too.

## Source map

| Source | Inspect for |
| --- | --- |
| [Core contract and consumer](../../core/agent/adaptive_interruption.go) | Validation, correlation, metrics, fallback, transcript release |
| [Pipeline](../../core/agent/pipeline_agent.go) | Stream creation, input ordering, result consumption, shutdown |
| [Activity](../../core/agent/agent_activity.go) | Audible speech and VAD boundaries, cooldown and transcript ownership |
| [Events](../../core/agent/events.go) | Verdict fields and JSON units |
| [Core tests](../../core/agent/adaptive_interruption_test.go) and [transcript tests](../../core/agent/adaptive_transcript_test.go) | Core behavior and timed/untimed STT regressions |
| [LiveKit adapter tests](../../adapter/livekit/adaptive_interruption_test.go) | Local WebSocket protocol and lifecycle examples |

## Implementation prompt

Copy this prompt and replace the bracketed task inputs before use. The fields are
intentional inputs to a future implementation task.

```text
Implement an adaptive interruption detector in RTP Agent.

Task inputs:
- Provider and existing adapter package: [provider; adapter/package]
- Authoritative model/protocol source and revision: [URL or local path; revision]
- Input format and required history: [sample rate; channels; encoding; window]
- Inference policy: [cadence; threshold; overlap-end behavior; deadlines]
- Provider quota and request-count definition: [unit; window; scope; counted stage]
- Composition target: [app selection or example consumer]

Read AGENTS.md, ARCHITECTURE.md, DEVELOPMENT.md, and
docs/features/adaptive-interruption-adapters.md. Use the repository skill
rtp-agent-adaptive-interruption. Verify the current core interfaces and tests.

Implement agent.InterruptionDetector and agent.InterruptionStream in the provider
package. Follow existing adapter conventions and add compile-time assertions.
Core owns speech/overlap boundaries, cooldowns, playback interruption, transcript
holding/filtering/release, endpointing, telemetry, and VAD fallback.

Confirm that the audio-only interface supports this model. If it requires STT
text, identify the missing capability and scope the core extension before
implementing it; PushTranscript is not an existing method. If a new standalone
package cannot pass the current architecture contract, identify the required
architecture change rather than adding a dummy capability.

Keep inference policy provider-specific. Preserve exact overlap correlation,
ignore stale/duplicate replies, report agent-ended outcomes as inconclusive, and
count usage once per overlap. Propagate operational failures as errors so core
can fall back. Bound I/O, define queue behavior, propagate context cancellation,
and make Close safe and sufficient to stop all owned workers.

Add offline tests for audio conversion, lifecycle ordering, verdicts, stale
responses, usage, failures, bursts, cancellation, blocked consumers, and cleanup.
Wire the detector into the requested composition target and document its options.
Run focused tests, race checks, and the architecture guard; follow DEVELOPMENT
for broader checks and any explicit reference-parity evidence. Use no real
credentials or paid provider calls in default tests.

Deliver a focused diff, a composition example, and a concise report of behavior,
source/revision evidence, commands run, and remaining limitations. Keep unrelated
runtime changes and generic provider scaffolding outside this task.
```
