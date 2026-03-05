---
id: hume
title: Hume
---

# Hume TTS Adapter for RTP Agent

Hume AI provides empathetic TTS capabilities, allowing agents to synthesize voices that reflect specific emotional tones and nuances.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/hume
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Hume developer documentation for acquiring the necessary API keys and tokens.

```env
HUME_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Hume TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/hume"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Hume TTS adapter
	ttsProvider, err := hume.NewProvider(
		os.Getenv("HUME_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize hume adapter: %v", err)
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
