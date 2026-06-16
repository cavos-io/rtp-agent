---
id: quickstart
title: Quickstart
---

# Quickstart

This tutorial runs the checked-in basic voice agent. It uses the app composition path that exists in `examples/voice_agents/basic_agent`.

## Prerequisites

- Go matching the repository `go.mod` toolchain.
- LiveKit credentials if you run against LiveKit transport.
- Model provider credentials for the configured providers.

The example defaults to LiveKit Inference providers in `basicagent.ConfigFromEnv`:

- LLM provider: `livekit`, model `openai/gpt-4.1-mini`
- STT provider: `livekit`, model `deepgram/nova-3`
- VAD provider: `silero`
- TTS provider: `livekit`, model `cartesia/sonic-3`

## Configure environment

Create a `.env` file in the repository root or export variables in your shell:

```bash
LIVEKIT_URL=wss://your-project.livekit.cloud
LIVEKIT_API_KEY=your_api_key
LIVEKIT_API_SECRET=your_api_secret
RTP_AGENT_INSTRUCTIONS="You are a helpful realtime voice agent."
```

Provider-specific variables depend on your selected model providers. For LiveKit Inference, the app reads `LIVEKIT_API_KEY` and `LIVEKIT_API_SECRET`.

## Run the example

```bash
go run ./examples/voice_agents/basic_agent
```

The example constructs the app with:

```go
package main

import (
	"context"
	"fmt"

	basicagent "github.com/cavos-io/rtp-agent/examples/voice_agents/basic_agent/basicagent"
	"github.com/cavos-io/rtp-agent/interface/cli"
)

func main() {
	rtpApp, err := basicagent.NewApp(basicagent.ConfigFromEnv())
	if err != nil {
		panic(err)
	}
	defer rtpApp.Close(context.Background())

	cli.RunApp(rtpApp.Server, func(ctx context.Context) (string, error) {
		summary, err := rtpApp.EvaluateSession(ctx, nil)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("score=%.2f\n", summary.Score), nil
	})
}
```

That code is adapted from `examples/voice_agents/basic_agent/main.go`. The important API facts are `app.Init`/`app.NewApp`, `basicagent.NewApp`, and `cli.RunApp`.

## Change models

Set provider and model environment variables before running:

```bash
RTP_AGENT_LLM_PROVIDER=openai
RTP_AGENT_LLM_MODEL=gpt-4.1-mini
OPENAI_API_KEY=your_openai_key

RTP_AGENT_STT_PROVIDER=deepgram
RTP_AGENT_STT_MODEL=nova-3
DEEPGRAM_API_KEY=your_deepgram_key

RTP_AGENT_TTS_PROVIDER=openai
RTP_AGENT_TTS_MODEL=gpt-4o-mini-tts
RTP_AGENT_TTS_VOICE=alloy
```

Only use provider names that are wired in `app/app.go` or construct adapters directly in Go.
