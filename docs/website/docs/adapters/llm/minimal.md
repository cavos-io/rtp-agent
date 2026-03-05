---
id: minimal
title: Minimal
---

# Minimal LLM Adapter for RTP Agent

The Minimal adapter provides a lightweight implementation for basic LLM needs without the overhead of larger frameworks.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/minimal
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Minimal developer documentation for acquiring the necessary API keys and tokens.

```env
MINIMAL_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Minimal LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/minimal"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Minimal LLM adapter
	llmProvider, err := minimal.NewProvider(
		os.Getenv("MINIMAL_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize minimal adapter: %v", err)
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
