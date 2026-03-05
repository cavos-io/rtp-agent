---
id: telnyx
title: Telnyx
---

# Telnyx TTS Adapter for RTP Agent

Telnyx provides seamless integration with Telnyx's Text-to-Speech (TTS) services, allowing your RTP Agent to generate human-like synthetic voices in real-time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/telnyx
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Telnyx developer documentation for acquiring the necessary API keys and tokens.

```env
TELNYX_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Telnyx TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/telnyx"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Telnyx TTS adapter
	ttsProvider, err := telnyx.NewProvider(
		os.Getenv("TELNYX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize telnyx adapter: %v", err)
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
