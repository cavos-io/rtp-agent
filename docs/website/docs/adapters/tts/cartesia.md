---
id: cartesia
title: Cartesia
---

# Cartesia TTS Adapter for RTP Agent

Cartesia is a high-speed, high-quality speech synthesis platform featuring the 'Sonic' model. It is designed for ultra-low latency, making it perfect for real-time interactive agents that require immediate vocal responses.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/cartesia
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Cartesia developer documentation for acquiring the necessary API keys and tokens.

```env
CARTESIA_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Cartesia TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/cartesia"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Cartesia TTS adapter
	ttsProvider, err := cartesia.NewProvider(
		os.Getenv("CARTESIA_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize cartesia adapter: %v", err)
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
