---
id: asyncai
title: AsyncAI
---

# AsyncAI TTS Adapter for RTP Agent

AsyncAI provides high-performance voice synthesis services designed for scale and consistency in multi-agent environments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/asyncai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the AsyncAI developer documentation for acquiring the necessary API keys and tokens.

```env
ASYNCAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the AsyncAI TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/asyncai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the AsyncAI TTS adapter
	ttsProvider, err := asyncai.NewProvider(
		os.Getenv("ASYNCAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize asyncai adapter: %v", err)
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
