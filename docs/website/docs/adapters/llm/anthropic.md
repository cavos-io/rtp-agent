---
id: anthropic
title: Anthropic
---

# Anthropic LLM Adapter for RTP Agent

Anthropic provides the Claude 3 family of models (Opus, Sonnet, and Haiku), known for their safety-first approach, high reasoning capabilities, and long context windows.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/anthropic
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Anthropic developer documentation for acquiring the necessary API keys and tokens.

```env
ANTHROPIC_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Anthropic LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/anthropic"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Anthropic LLM adapter
	llmProvider, err := anthropic.NewProvider(
		os.Getenv("ANTHROPIC_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize anthropic adapter: %v", err)
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
