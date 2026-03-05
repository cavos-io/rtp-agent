---
id: rime
title: Rime
---

# Rime TTS Adapter for RTP Agent

Rime (by Rime AI) is a fast, high-quality TTS engine designed for modern AI applications. It offers a selection of natural voices that can be synthesized at high speeds for real-time conversational agents.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/rime
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Rime developer documentation for acquiring the necessary API keys and tokens.

```env
RIME_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Rime TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/rime"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Rime TTS adapter
	ttsProvider, err := rime.NewProvider(
		os.Getenv("RIME_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize rime adapter: %v", err)
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
