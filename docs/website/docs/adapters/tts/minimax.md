---
id: minimax
title: MiniMax
---

# MiniMax TTS Adapter for RTP Agent

MiniMax TTS provides high-quality, expressive voice synthesis, enabling agents to communicate with human-like prosody and emotion.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/minimax
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the MiniMax developer documentation for acquiring the necessary API keys and tokens.

```env
MINIMAX_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the MiniMax TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/minimax"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the MiniMax TTS adapter
	ttsProvider, err := minimax.NewProvider(
		os.Getenv("MINIMAX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize minimax adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithTTS(ttsProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
