# AGENTS.md

## Primary Mission

This repository is a Go implementation effort for LiveKit Agents-style runtime behavior.
Use `refs/agents/livekit-agents` as the behavioral reference and port its useful
functionality into the Go codebase incrementally.

Do not duplicate broad project setup or architecture guidance here. Read:

- `README.md` for project purpose, setup, and CLI usage.
- `ARCHITECTURE.md` for layer boundaries and dependency direction.

## Reference-To-Go Map

Use this map when deciding where reference behavior belongs:

| LiveKit Python reference | Go destination |
| --- | --- |
| `livekit/agents/worker.py`, `job.py` | `interface/worker` |
| `livekit/agents/ipc/*` | `interface/worker/ipc` |
| `livekit/agents/cli/*` | `interface/cli` |
| `livekit/agents/voice/agent.py` | `core/agent/agent.go` |
| `livekit/agents/voice/agent_session.py` | `core/agent/agent_session.go` |
| `livekit/agents/voice/agent_activity.py` | `core/agent/agent_activity.go` |
| `livekit/agents/voice/generation.py` | `core/agent/generation.go` |
| `livekit/agents/voice/room_io/*` | `interface/worker/room_io.go` |
| `livekit/agents/voice/recorder_io/*` | `interface/worker/recorder_io.go` |
| `livekit/agents/voice/transcription/*` | `core/agent/transcription.go` |
| `livekit/agents/voice/avatar/*` | `core/agent/avatar.go` plus provider adapters |
| `livekit/agents/voice/ivr/*` | `core/agent/ivr.go` and `core/beta/tools` |
| `livekit/agents/llm/*` | `core/llm` |
| `livekit/agents/stt/*` | `core/stt` |
| `livekit/agents/tts/*` | `core/tts` |
| `livekit/agents/vad.py` | `core/vad` |
| `livekit/agents/inference/*` | `core/inference` for compatibility shims |
| `livekit/agents/evals/*` | `core/evals` |
| `livekit/agents/metrics/*`, `telemetry/*` | `library/telemetry` |
| `livekit/agents/tokenize/*` | `library/tokenize` |
| `livekit/agents/utils/*` | `library/utils` or a narrower existing package |
| `livekit-plugins/*` | `adapter/<provider>` |

## Porting Rules

- Start from the Python reference behavior, then implement the Go version in the
  package that matches the map above.
- Preserve public concepts and lifecycle semantics where practical: worker,
  job context, agent, session, activity, tools, streaming LLM/STT/TTS, VAD,
  interruption, room I/O, telemetry, and plugin/provider boundaries.
- Keep `core` provider-agnostic. Provider-specific API details belong in
  `adapter/<provider>`.
- Keep LiveKit transport, room connection, and worker protocol concerns in
  `interface/worker`.
- Keep CLI parsing and local developer commands in `interface/cli`.
- Prefer small Go interfaces around stable behavior over direct translations of
  Python inheritance patterns.
- Do not add new cross-layer imports that violate `.go-arch-lint.yml`.
- Do not edit `refs/agents/*` except when explicitly updating the vendored
  reference material.

## Working Workflow

For each parity task:

1. Inspect the matching Python reference files under `refs/agents/livekit-agents`.
2. Inspect the existing Go package and tests before changing code.
3. Identify whether the task is core behavior, transport glue, provider adapter,
   CLI behavior, or shared utility code.
4. Add or update focused tests near the Go package being changed.
5. Run the narrowest useful verification first, then broaden as risk increases.

Common verification commands:

```sh
go test ./...
go build ./...
go-arch-lint check
go-arch-lint mapping
```

If Go tries to write module cache files under a read-only home directory, use a
workspace or `/tmp` cache rather than changing project code to work around the
environment.

## Current Known Drift

- The repository is branded as `rtp-agent`, but the Go module path is currently
  `github.com/cavos-io/conversation-worker`.
- Several LiveKit reference areas already have partial Go equivalents. Treat
  existing Go code as the starting point and close behavioral gaps
  incrementally.
- `core/inference` is a compatibility boundary that may depend on adapters per
  `.go-arch-lint.yml`; avoid expanding that exception without a clear reason.

## Documentation Hygiene

Keep this file focused on instructions for future coding agents. Put user-facing
setup and feature documentation in `README.md` or the Docusaurus docs. Put
architecture policy in `ARCHITECTURE.md`.
