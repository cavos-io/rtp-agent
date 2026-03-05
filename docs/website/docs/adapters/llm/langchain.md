---
id: langchain
title: LangChain
---

# LangChain LLM Adapter for RTP Agent

The LangChain adapter allows you to utilize your existing LangChain-based logic and chains within the RTP Agent framework.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/langchain
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the LangChain developer documentation for acquiring the necessary API keys and tokens.

```env
LANGCHAIN_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the LangChain LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/langchain"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the LangChain LLM adapter
	llmProvider, err := langchain.NewProvider(
		os.Getenv("LANGCHAIN_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize langchain adapter: %v", err)
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
