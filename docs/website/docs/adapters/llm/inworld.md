---
id: inworld
title: Inworld
---

# Inworld LLM Adapter for RTP Agent

Inworld AI provides a character engine that powers digital personas with personality, memory, and situational awareness.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/inworld
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Inworld developer documentation for acquiring the necessary API keys and tokens.

```env
INWORLD_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Inworld LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/inworld"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Inworld LLM adapter
	llmProvider, err := inworld.NewProvider(
		os.Getenv("INWORLD_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize inworld adapter: %v", err)
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
