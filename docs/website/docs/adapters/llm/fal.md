---
id: fal
title: Fal AI
---

# Fal AI LLM Adapter for RTP Agent

Fal AI provides ultra-fast inference for generative LLMs, ensuring that your agent's reasoning process doesn't introduce lag into the conversation.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/fal
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Fal AI developer documentation for acquiring the necessary API keys and tokens.

```env
FAL_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Fal AI LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/fal"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Fal AI LLM adapter
	llmProvider, err := fal.NewProvider(
		os.Getenv("FAL_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize fal adapter: %v", err)
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
