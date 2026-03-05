---
id: simli
title: Simli
---

# Simli LLM Adapter for RTP Agent

Simli's models are optimized for driving real-time digital humans, coordinating LLM responses with visual avatar synthesis.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/simli
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Simli developer documentation for acquiring the necessary API keys and tokens.

```env
SIMLI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Simli LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/simli"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Simli LLM adapter
	llmProvider, err := simli.NewProvider(
		os.Getenv("SIMLI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize simli adapter: %v", err)
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
