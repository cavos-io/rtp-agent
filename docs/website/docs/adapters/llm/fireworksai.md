---
id: fireworksai
title: Fireworks AI
---

# Fireworks AI LLM Adapter for RTP Agent

Fireworks AI provides a fast inference platform for the latest open models, optimized for low latency and high throughput in real-time applications.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/fireworksai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Fireworks AI developer documentation for acquiring the necessary API keys and tokens.

```env
FIREWORKSAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Fireworks AI LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/fireworksai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Fireworks AI LLM adapter
	llmProvider, err := fireworksai.NewProvider(
		os.Getenv("FIREWORKSAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize fireworksai adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithLLM(llmProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
