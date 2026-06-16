---
id: configuration
title: Configuration and deployment
---

# Configuration and deployment

Runtime configuration is centralized in `app.AppConfig`. Use `app.DefaultConfigFromEnv()` to load environment variables, then override fields in Go when needed.

```go
package main

import "github.com/cavos-io/rtp-agent/app"

func buildApp() (*app.App, error) {
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = "You are a concise support agent."
	cfg.LLMProvider = "openai"
	cfg.LLMModel = "gpt-4.1-mini"

	rtpApp, err := app.Init(cfg)
	if err != nil {
		return nil, err
	}

	return rtpApp, nil
}
```

## Core environment variables

Common variables include:

- `LIVEKIT_URL`
- `LIVEKIT_API_KEY`
- `LIVEKIT_API_SECRET`
- `RTP_AGENT_INSTRUCTIONS`
- `RTP_AGENT_LLM_PROVIDER`
- `RTP_AGENT_LLM_MODEL`
- `RTP_AGENT_STT_PROVIDER`
- `RTP_AGENT_STT_MODEL`
- `RTP_AGENT_VAD_PROVIDER`
- `RTP_AGENT_TTS_PROVIDER`
- `RTP_AGENT_TTS_MODEL`
- `RTP_AGENT_TTS_VOICE`
- `RTP_AGENT_REALTIME_PROVIDER`
- `RTP_AGENT_REALTIME_MODEL`
- `RTP_AGENT_AVATAR_PROVIDER`
- `RTP_AGENT_TURN_DETECTOR_PROVIDER`

Provider API keys are provider-specific, for example `OPENAI_API_KEY`, `DEEPGRAM_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`, and `LIVEKIT_API_KEY`.

## Deployment shape

Build and run a Go binary that initializes `app.App` and passes its server to `cli.RunApp`. Worker transport, credentials, and provider selection should come from environment or deployment configuration.

Do not document a deployment target as supported unless the repository contains the corresponding server, transport, or integration code.
