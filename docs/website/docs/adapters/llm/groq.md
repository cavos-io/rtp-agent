---
id: groq
title: Groq
---

# Groq LLM Adapter for RTP Agent

Groq provides an LPU (Language Processing Unit) inference engine that delivers industry-leading speeds for open models like Llama 3 and Mixtral, enabling near-instant agent responses.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/groq
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Groq developer documentation for acquiring the necessary API keys and tokens.

```env
GROQ_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Groq LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/groq"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Groq LLM adapter
	llmProvider, err := groq.NewProvider(
		os.Getenv("GROQ_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize groq adapter: %v", err)
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
