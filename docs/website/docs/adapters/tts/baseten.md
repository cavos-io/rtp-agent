---
id: baseten
title: Baseten
---

# Baseten TTS Adapter for RTP Agent

Baseten provides seamless integration with Baseten's Text-to-Speech (TTS) services, allowing your RTP Agent to generate human-like synthetic voices in real-time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/baseten
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Baseten developer documentation for acquiring the necessary API keys and tokens.

```env
BASETEN_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Baseten TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/baseten"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Baseten TTS adapter
	ttsProvider, err := baseten.NewProvider(
		os.Getenv("BASETEN_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize baseten adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithTTS(ttsProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
