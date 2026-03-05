---
id: xai
title: xAI
---

# xAI Realtime Adapter for RTP Agent

The xAI adapter provides seamless integration with xAI's Multimodal Realtime API, allowing your RTP Agent to process voice and text natively with ultra-low latency.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/xai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the xAI developer documentation for acquiring the necessary API keys and tokens.

```env
XAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the xAI Realtime adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/xai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the xAI Realtime adapter
	realtimeProvider, err := xai.NewRealtimeModel(
		os.Getenv("XAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize xAI adapter: %v", err)
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
