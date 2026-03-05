---
id: neuphonic
title: Neuphonic
---

# Neuphonic TTS Adapter for RTP Agent

Neuphonic provides a streaming-first, low-latency TTS service. It is specifically architected to minimize time-to-first-byte, ensuring that voice synthesis starts almost immediately after text is provided.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/neuphonic
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Neuphonic developer documentation for acquiring the necessary API keys and tokens.

```env
NEUPHONIC_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Neuphonic TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/neuphonic"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Neuphonic TTS adapter
	ttsProvider, err := neuphonic.NewProvider(
		os.Getenv("NEUPHONIC_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize neuphonic adapter: %v", err)
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
