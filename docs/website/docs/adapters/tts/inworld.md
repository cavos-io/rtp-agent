---
id: inworld
title: Inworld
---

# Inworld TTS Adapter for RTP Agent

Inworld TTS provides specialized voice synthesis for interactive digital personas, optimized for game environments and real-time storytelling.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/inworld
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Inworld developer documentation for acquiring the necessary API keys and tokens.

```env
INWORLD_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Inworld TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/inworld"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Inworld TTS adapter
	ttsProvider, err := inworld.NewProvider(
		os.Getenv("INWORLD_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize inworld adapter: %v", err)
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
