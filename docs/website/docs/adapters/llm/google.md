---
id: google
title: Google
---

# Google LLM Adapter for RTP Agent

Google provides access to the Gemini family of models (Pro and Flash), which are natively multimodal and optimized for high-speed, low-cost intelligence.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/google
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Google developer documentation for acquiring the necessary API keys and tokens.

```env
GOOGLE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Google LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/google"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Google LLM adapter
	llmProvider, err := google.NewProvider(
		os.Getenv("GOOGLE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize google adapter: %v", err)
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
