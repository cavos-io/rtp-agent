---
id: smallestai
title: Smallest AI
---

# Smallest AI LLM Adapter for RTP Agent

Smallest AI focuses on compact, highly efficient LLMs that can run with minimal latency and reduced computational requirements.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/smallestai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Smallest AI developer documentation for acquiring the necessary API keys and tokens.

```env
SMALLESTAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Smallest AI LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/smallestai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Smallest AI LLM adapter
	llmProvider, err := smallestai.NewProvider(
		os.Getenv("SMALLESTAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize smallestai adapter: %v", err)
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
