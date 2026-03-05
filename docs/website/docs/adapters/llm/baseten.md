---
id: baseten
title: Baseten
---

# Baseten LLM Adapter for RTP Agent

Baseten allows you to deploy and scale your custom fine-tuned LLMs with high-performance infrastructure optimized for real-time inference.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/baseten
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Baseten developer documentation for acquiring the necessary API keys and tokens.

```env
BASETEN_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Baseten LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/baseten"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Baseten LLM adapter
	llmProvider, err := baseten.NewProvider(
		os.Getenv("BASETEN_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize baseten adapter: %v", err)
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
