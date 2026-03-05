---
id: asyncai
title: AsyncAI
---

# AsyncAI LLM Adapter for RTP Agent

AsyncAI provides seamless integration with AsyncAI's Large Language Models (LLMs), allowing your RTP Agent to converse with users intelligently in real-time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/asyncai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the AsyncAI developer documentation for acquiring the necessary API keys and tokens.

```env
ASYNCAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the AsyncAI LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/asyncai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the AsyncAI LLM adapter
	llmProvider, err := asyncai.NewProvider(
		os.Getenv("ASYNCAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize asyncai adapter: %v", err)
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
