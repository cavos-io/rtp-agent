---
id: ultravox
title: Ultravox
---

# Ultravox Realtime Adapter for RTP Agent

The Ultravox adapter provides seamless integration with Ultravox's Multimodal Realtime API, allowing your RTP Agent to process voice and text natively with ultra-low latency.

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

Below is a basic conceptual example demonstrating how to initialize the Ultravox Realtime adapter within an RTP Agent session:

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

	// Initialize the Ultravox Realtime adapter
	realtimeProvider, err := ultravox.NewRealtimeModel(
		os.Getenv("ULTRAVOX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize Ultravox adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewMultimodalSession(
		agent.WithRealtimeModel(realtimeProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
