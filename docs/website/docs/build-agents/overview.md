---
id: overview
title: Build Agents
---

# Build Agents

Status: **partial**.

Use this section when you want to build an agent with the Go runtime in this repository.

The supported path is code-first:

1. Configure an `app.AppConfig`.
2. Create an `app.App` with `app.Init` or an example-specific constructor.
3. Attach an `agent.Agent` to an `agent.AgentSession`.
4. Run the worker server through `interface/cli.RunApp` or your own process supervisor.

The API surface is intentionally smaller than the LiveKit Agents Python and Node.js docs. Pages in this section keep the LiveKit information architecture so you can orient yourself, but each page states whether `rtp-agent` implements the capability, implements part of it, or differs by design.

## What you can build now

You can build a voice agent that joins LiveKit rooms, uses LLM/STT/TTS/VAD providers, calls Go tools, tracks session state, and runs under the worker lifecycle. The checked-in basic agent is the fastest working reference.

Start with [Voice AI quickstart](./get-started/voice-ai-quickstart.md), then use the model and provider pages when you need to change LLM, STT, TTS, realtime, or avatar components.

## What is not documented as available

Do not assume a LiveKit-hosted Agent Builder, web embed widget, full frontend SDK guide, or production telephony product workflow exists in this Go repo. Those pages are present only as status pages until source code and tests implement the capability.

Evidence:

- `app/app.go`
- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `interface/worker/server.go`
- `interface/worker/room_io.go`
- `scripts/parity-fixtures/test-cases.tsv`
