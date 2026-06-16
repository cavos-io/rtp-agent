---
id: packages
title: Package reference
---

# Package reference

Use this page to find the package that owns a behavior before reading or changing source.

## Application

- `app`: configuration, provider wiring, app lifecycle, evaluation.
- `cmd`: default binary entrypoint using `app.Init(app.DefaultConfigFromEnv())`.

## Core

- `core/agent`: agents, sessions, activities, generation, events, interruptions, transcription, avatars, reports.
- `core/llm`: chat context, LLM interface, realtime model interface, tools, MCP, errors, fallback adapter.
- `core/stt`: STT interface, stream adapter, fallback adapter, multi-speaker adapter, errors.
- `core/tts`: TTS interface, stream synthesis, pacing, text filters, fallback adapter, errors.
- `core/vad`: VAD interface.
- `core/evals`: evaluation and judging.

## Interface

- `interface/cli`: command-line runtime, local console, dev watcher.
- `interface/worker`: agent server, jobs, transports, room I/O, recorder I/O, IPC.

## Adapters

Adapters live under `adapter/<provider>` and implement provider-specific details. Capability is indicated by source files such as `llm.go`, `stt.go`, `tts.go`, `realtime.go`, `avatar.go`, or `vad.go`.

## Boundary rule

Keep provider-specific API details in `adapter/<provider>`. Keep provider-agnostic behavior in `core`. Keep room, worker, CLI, transport, and process lifecycle behavior in `interface`.
