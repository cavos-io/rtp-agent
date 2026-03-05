---
id: speechify
title: Speechify
---

# Speechify TTS Adapter for RTP Agent

Speechify is a well-known AI voice platform that offers a massive library of high-quality voices. Originally built for reading assistance, their API now provides developers with easy access to clear, engaging synthetic speech.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/speechify
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Speechify developer documentation for acquiring the necessary API keys and tokens.

```env
SPEECHIFY_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Speechify TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/speechify"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Speechify TTS adapter
	ttsProvider, err := speechify.NewProvider(
		os.Getenv("SPEECHIFY_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize speechify adapter: %v", err)
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
