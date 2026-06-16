---
id: voice-ai-quickstart
title: Voice AI quickstart
---

# Voice AI quickstart

Status: **implemented** as a Go example, not as a cloud wizard.

Evidence:

- `examples/voice_agents/basic_agent/main.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`
- `app/app.go`

Run the basic agent:

```bash
LIVEKIT_URL=wss://your-project.livekit.cloud
LIVEKIT_API_KEY=your_api_key
LIVEKIT_API_SECRET=your_api_secret
go run ./examples/voice_agents/basic_agent
```

The example configures a LiveKit Inference pipeline in `basicagent.ConfigFromEnv()` and then calls `basicagent.NewApp`.

Relevant APIs:

- `app.DefaultConfigFromEnv()`
- `app.Init()` / `app.NewApp()`
- `agent.NewAgent()`
- `agent.NewAgentSession()`
- `cli.RunApp()`

