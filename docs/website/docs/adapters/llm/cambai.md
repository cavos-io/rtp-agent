---
id: cambai
title: Camb.ai
---

# Camb.ai LLM Adapter for RTP Agent

Camb.ai provides seamless integration with Camb.ai's Large Language Models (LLMs), allowing your RTP Agent to converse with users intelligently in real-time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/cambai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Camb.ai developer documentation for acquiring the necessary API keys and tokens.

```env
CAMBAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Camb.ai LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/cambai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Camb.ai LLM adapter
	llmProvider, err := cambai.NewProvider(
		os.Getenv("CAMBAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize cambai adapter: %v", err)
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
