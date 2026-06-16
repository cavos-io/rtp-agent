---
id: introduction
title: Introduction
slug: /
---

# rtp-agent

`rtp-agent` is a Go runtime for building realtime AI agents. It follows the useful runtime behavior of LiveKit Agents where that behavior is implemented and tested in this repository: agent/session orchestration, streaming LLM/STT/TTS boundaries, tools, interruptions, worker lifecycle, room I/O, telemetry, and provider adapters.

This documentation describes the Go source API at tag `v0.0.67`. The tag exists in this checkout. The documentation branch may contain docs-only commits after that tag; the diff from `v0.0.67` to this documentation update contains no Go source, `go.mod`, or `go.sum` changes.

## What is implemented

The public composition path is:

1. Build an `app.AppConfig`, usually from `app.DefaultConfigFromEnv()`.
2. Call `app.Init` or `app.NewApp`.
3. Run the resulting `worker.AgentServer` through `interface/cli.RunApp`.

For lower-level composition, the source exposes `agent.NewAgent`, `agent.NewAgentSession`, model interfaces in `core/llm`, `core/stt`, and `core/tts`, and provider constructors in `adapter/<provider>`.

## Source of truth

The Go source is the API contract for this version. These docs intentionally avoid conceptual APIs that are not present in source, including `NewProvider`, `agent.NewSession`, `agent.WithLLM`, `agent.WithSTT`, and `agent.WithTTS`.

Behavioral parity with LiveKit Agents is a project goal, not a blanket compatibility claim. When parity-sensitive behavior is documented, it should be backed by source, tests, or a row in `scripts/parity-fixtures/test-cases.tsv`.

## Documentation map

- **Tutorials**: start with the basic agent and run it locally.
- **How-to guides**: configure models, tools, workers, and runtime behavior.
- **Explanation**: understand architecture, lifecycle, provider boundaries, and parity.
- **Reference**: inspect packages, constructors, environment variables, and provider capabilities.
