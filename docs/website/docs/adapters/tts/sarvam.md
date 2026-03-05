---
id: sarvam
title: Sarvam AI
---

# Sarvam AI TTS Adapter for RTP Agent

Sarvam AI is focused on providing state-of-the-art models tailored for Indic languages, ensuring high-quality, culturally aware interactions across a wide variety of dialects.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/sarvam
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Sarvam AI developer documentation for acquiring the necessary API keys and tokens.

```env
SARVAM_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Sarvam AI TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/sarvam"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Sarvam AI TTS adapter
	ttsProvider, err := sarvam.NewProvider(
		os.Getenv("SARVAM_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize sarvam adapter: %v", err)
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
