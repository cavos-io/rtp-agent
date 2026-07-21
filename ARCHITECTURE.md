# RTP Agent Architecture

## Purpose and authority

RTP Agent is a Go-first runtime for realtime voice and multimodal agents. It combines provider-neutral agent behavior with pluggable VAD, STT, LLM, TTS, avatar, computer-tool, and realtime-model integrations.

This document explains the repository's architectural intent and runtime ownership. [`.architecture.yaml`](.architecture.yaml) is the executable definition of package boundaries, dependency rules, and adapter file contracts. Keep both files synchronized whenever those rules change.

## Repository structure

```text
cmd/             Executable entry points
app/             Application composition and provider selection
interface/cli/   CLI commands and local developer interaction
interface/worker Worker, job, room, and transport lifecycles
core/            Provider-neutral agent runtime and capability contracts
core/**/model/   Low-level models with stricter dependency isolation
adapter/         Provider integrations and capability implementations
library/         Shared, domain-neutral utilities
examples/        Example consumers; outside the enforced component graph
```

## Component responsibilities

### `cmd`

Keep executable entry points thin. Parse process-level inputs, then delegate startup to `app` or `interface/cli`. Do not place provider behavior or agent orchestration here.

### `app`

The composition layer may know concrete adapters. It selects providers, builds dependencies, configures the runtime, and connects CLI or worker entry points to core behavior.

### `interface/cli`

Own command parsing and local developer workflows. It may invoke worker and core APIs, but provider-specific protocols belong in adapters.

### `interface/worker`

Own worker, job, room, participant, and media-transport lifecycles. LiveKit, Agora, and IPC implementations translate transport events into core operations. They do not own provider capability behavior.

### `core`

Own provider-neutral contracts and runtime behavior, including agents, sessions, audio, VAD, STT, LLM, TTS, evaluation, and beta workflows. Core packages depend inward on other core packages and models, never on concrete providers.

Packages matched by `core/**/model` contain low-level models. Their stricter boundary allows dependencies only on other model packages and the common library component.

### `adapter`

Own provider integrations. Adapters implement core capability contracts, translate provider events and payloads, and contain provider-specific authentication, configuration, transport, and error mapping. They must not redefine provider-neutral interfaces.

### `library`

Own reusable, domain-neutral utilities. `library` is a common component available to every architectural component, but it must not become a home for agent rules or provider behavior.

## Enforced dependency direction

The following table mirrors `.architecture.yaml`:

| Component | May depend on |
| --- | --- |
| `cmd` | `app`, `interface_cli` |
| `app` | `adapter`, `core`, `core_model`, `interface_cli`, `interface_worker` |
| `interface_cli` | `interface_cli`, `interface_worker`, `core`, `core_model` |
| `interface_worker` | `interface_worker`, `core`, `core_model` |
| `adapter` | `adapter`, `core`, `core_model` |
| `core` | `core`, `core_model` |
| `core_model` | `core_model` |
| `library` | `library` |

Because `library` is configured as a common component, every component may also depend on it. Dependencies not listed above are forbidden. `examples` are consumers rather than architecture components and are not included in this enforced graph.

## Runtime composition and streaming flow

The typical runtime flow is:

```text
cmd
  -> app or interface/cli
  -> interface/worker and core agent/session runtime
  -> core capability contract
  -> provider adapter
  -> external provider
```

Provider events and media flow back through the adapter into the core runtime, then through the worker transport to the room or client.

Streaming ownership follows component boundaries:

- Core owns capability contracts, agent/session state, and provider-neutral lifecycle decisions.
- Adapters own provider connections, wire protocols, event translation, and provider-specific retries or errors.
- Workers own job and room transport lifecycles, participant I/O, and delivery to or from the core runtime.
- Cancellation must propagate through `context.Context`. A component closes only channels, streams, and goroutines it creates, and must preserve ordering and backpressure without blocking transport callbacks indefinitely.

## Adapter contracts

Every directory matched by `adapter/*` must contain `plugin.go` with:

- `PluginTitle`, matching `rtp-agent.plugins.<package>`
- `PluginVersion`, using a `vMAJOR.MINOR.PATCH` value
- `PluginPackage`, matching `rtp-agent.plugins.<package>`

A provider adapter must implement at least one capability file unless it is one of the explicitly exempt utility adapters: `blingfire`, `browser`, `hamming`, `krisp`, `nltk`, or `pipecat`.

Capability files use canonical public names and have a same-name test file:

| File | Canonical struct | Constructor | Option type | Test file |
| --- | --- | --- | --- | --- |
| `vad.go` | `VAD` | `NewVAD` | `VADOption` | `vad_test.go` |
| `stt.go` | `STT` | `NewSTT` | `STTOption` | `stt_test.go` |
| `llm.go` | `LLM` | `NewLLM` | `LLMOption` | `llm_test.go` |
| `tts.go` | `TTS` | `NewTTS` | `TTSOption` | `tts_test.go` |
| `avatar.go` | `Avatar` | `NewAvatar` | `AvatarOption` | `avatar_test.go` |
| `computer_tool.go` | `ComputerTool` | `NewComputerTool` | none | `computer_tool_test.go` |
| `realtime.go` | `RealtimeModel` | `NewRealtimeModel` | `RealtimeOption` | `realtime_test.go` |

Capability files must not declare exported interfaces or provider-prefixed canonical structs. Core defines the interfaces; adapters provide implementations. Deprecated provider-prefixed aliases may remain temporarily for source compatibility, but new code must use the canonical names.

The executable contract contains narrow compatibility exceptions:

- Spitch STT and Clova TTS do not require an option type.
- `LLMOption` is required only for adapters whose current API uses explicit options: Anthropic, AWS, Google, Groq, LiveKit, Mistral AI, OpenAI, Sarvam, and Telnyx.
- Azure's LLM API uses canonical aliases and delegates construction to the OpenAI-compatible implementation.
- `AvatarOption` is currently required only for Runway.

These exceptions describe current APIs; do not generalize them to new adapters.

## Testing and parity guidance

- Every capability file requires its contract test file. Constructor tests should verify meaningful defaults, option application, or interface conformance rather than constructor existence alone.
- Keep unit tests deterministic and offline. Provider network behavior belongs in explicitly scoped integration tests.
- Use `refs/agents/livekit-agents` and `refs/agents/livekit-plugins` as behavioral references when migrating capabilities. Match required behavior, lifecycle, and event semantics; do not copy structure merely for symbol parity.
- Run `go tool go-file-arch -config .architecture.yaml ./...` after structural changes. Run the relevant package tests, and use `scripts/go-test-all.sh` for repository-wide verification when code changes cross packages.

## Change rules

1. Keep changes within the dependency graph and component responsibilities above.
2. Update `.architecture.yaml` and this document together when an enforced boundary or adapter contract changes.
3. Add new provider behavior in `adapter`; add provider-neutral contracts or lifecycle behavior in `core`; compose concrete implementations in `app`.
4. Preserve compatibility with deprecated aliases only while migration requires it. Do not use deprecated names in new code.
5. Prefer incremental capability-batched migrations with focused tests over unrelated repository-wide rewrites.
