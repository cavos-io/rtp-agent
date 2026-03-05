---
id: openai
title: OpenAI
---

# OpenAI Realtime Adapter for RTP Agent

OpenAI's Realtime API provides a websocket-based interface for low-latency, multimodal interactions, allowing for native speech-to-speech conversations without separate STT/TTS steps.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/openai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the OpenAI developer documentation for acquiring the necessary API keys and tokens.

```env
OPENAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the OpenAI Realtime adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the OpenAI Realtime adapter
	realtimeProvider, err := openai.NewRealtimeModel(
		os.Getenv("OPENAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize openai adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithRealtimeModel(realtimeProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
