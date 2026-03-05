---
id: fal
title: Fal AI
---

# Fal AI STT Adapter for RTP Agent

Fal AI provides ultra-fast inference for generative media models, including real-time speech-to-text capabilities powered by optimized Whisper deployments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/fal
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Fal AI developer documentation for acquiring the necessary API keys and tokens.

```env
FAL_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Fal AI STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/fal"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Fal AI STT adapter
	sttProvider, err := fal.NewProvider(
		os.Getenv("FAL_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize fal adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithSTT(sttProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
