---
id: nvidia
title: NVIDIA
---

# NVIDIA LLM Adapter for RTP Agent

NVIDIA NIM (Inference Microservices) provides optimized access to a wide range of open and proprietary LLMs, accelerated by NVIDIA's high-performance hardware stack.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/nvidia
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the NVIDIA developer documentation for acquiring the necessary API keys and tokens.

```env
NVIDIA_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the NVIDIA LLM adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/nvidia"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the NVIDIA LLM adapter
	llmProvider, err := nvidia.NewProvider(
		os.Getenv("NVIDIA_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize nvidia adapter: %v", err)
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
