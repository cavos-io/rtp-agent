---
id: ultravox
title: Ultravox
---

# Ultravox TTS Adapter for RTP Agent

Ultravox (by Fixie.ai) is part of a new generation of end-to-end speech models. The adapter allows your agent to utilize Ultravox's expressive output capabilities for high-speed, fluid voice interactions.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/ultravox
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Ultravox developer documentation for acquiring the necessary API keys and tokens.

```env
ULTRAVOX_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Ultravox TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/ultravox"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Ultravox TTS adapter
	ttsProvider, err := ultravox.NewProvider(
		os.Getenv("ULTRAVOX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize ultravox adapter: %v", err)
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
