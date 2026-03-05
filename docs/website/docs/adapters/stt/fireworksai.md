---
id: fireworksai
title: Fireworks AI
---

# Fireworks AI STT Adapter for RTP Agent

Fireworks AI offers a high-performance inference platform for large language and speech models, providing low-latency transcription services for real-time agent workflows.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/fireworksai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Fireworks AI developer documentation for acquiring the necessary API keys and tokens.

```env
FIREWORKSAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Fireworks AI STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/fireworksai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Fireworks AI STT adapter
	sttProvider, err := fireworksai.NewProvider(
		os.Getenv("FIREWORKSAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize fireworksai adapter: %v", err)
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
