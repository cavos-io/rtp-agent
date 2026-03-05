---
id: elevenlabs
title: ElevenLabs
---

# ElevenLabs TTS Adapter for RTP Agent

ElevenLabs is the industry leader in emotionally expressive, human-like AI voices. Their models support high-fidelity voice cloning and multilingual synthesis, providing the most natural-sounding vocal interactions currently available.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/elevenlabs
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the ElevenLabs developer documentation for acquiring the necessary API keys and tokens.

```env
ELEVENLABS_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the ElevenLabs TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the ElevenLabs TTS adapter
	ttsProvider, err := elevenlabs.NewProvider(
		os.Getenv("ELEVENLABS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize elevenlabs adapter: %v", err)
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
