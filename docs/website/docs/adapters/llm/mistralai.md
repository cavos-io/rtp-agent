---
id: mistralai
title: Mistral AI
---

# Mistral AI LLM Adapter for RTP Agent

Mistral AI offers open-weights models like Mistral 7B and Mixtral 8x7B, as well as proprietary models that strike an excellent balance between performance and efficiency.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/mistralai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Mistral AI developer documentation for acquiring the necessary API keys and tokens.

```env
MISTRALAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Mistral AI LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/mistralai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Mistral AI LLM adapter
	llmProvider, err := mistralai.NewProvider(
		os.Getenv("MISTRALAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize mistralai adapter: %v", err)
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
