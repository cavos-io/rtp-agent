---
id: voice-ai-quickstart
title: Voice AI quickstart
---

# Voice AI quickstart

Status: **implemented** as a Go example, not as a cloud wizard.

This tutorial gets the checked-in voice agent running. It is the fastest source-backed way to verify your LiveKit credentials, model configuration, worker process, agent session, and tools.

## Prerequisites

You need:

- Go installed for this repository.
- A LiveKit server or LiveKit Cloud project.
- `LIVEKIT_URL`, `LIVEKIT_API_KEY`, and `LIVEKIT_API_SECRET`.

The example defaults to LiveKit Inference model provider names in `basicagent.ConfigFromEnv()`. Change those defaults in code, or override the app configuration with the `RTP_AGENT_*` variables supported by `app.DefaultConfigFromEnv()`.

## Run the agent

Set the required LiveKit credentials:

```bash
export LIVEKIT_URL=wss://your-project.livekit.cloud
export LIVEKIT_API_KEY=your_api_key
export LIVEKIT_API_SECRET=your_api_secret
```

Then run the basic agent:

```bash
go run ./examples/voice_agents/basic_agent
```

The program creates the app with `basicagent.NewApp(basicagent.ConfigFromEnv())`, defers `Close`, and hands the app's worker server to `cli.RunApp`.

## What the example demonstrates

- `app.DefaultConfigFromEnv()` reads runtime configuration.
- `app.Init()` creates the default app, worker server, agent session, and model components.
- `agent.NewAgent()` creates the custom "Kelly" agent.
- `AgentSession.UpdateAgent()` installs the custom agent in the running session.
- `llm.Tool` implementations expose callable tools to the LLM path.
- `Agent.OnEnter()` can trigger an initial reply with `GenerateReplyWithOptions`.

Use this example as a starting point for your own app. Copy the structure, not the exact provider defaults, unless the LiveKit Inference models used by the example match your deployment.

Evidence:

- `examples/voice_agents/basic_agent/main.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`
- `app/app.go`
