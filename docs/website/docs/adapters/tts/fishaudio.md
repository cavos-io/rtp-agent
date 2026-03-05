---
id: fishaudio
title: Fish Audio
---

# Fish Audio TTS Adapter for RTP Agent

Fish Audio offers high-performance TTS models that balance speed and quality. It provides a wide range of voices and is optimized for developers looking for robust, scalable voice synthesis solutions.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/fishaudio
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Fish Audio developer documentation for acquiring the necessary API keys and tokens.

```env
FISHAUDIO_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Fish Audio TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/fishaudio"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Fish Audio TTS adapter
	ttsProvider, err := fishaudio.NewProvider(
		os.Getenv("FISHAUDIO_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize fishaudio adapter: %v", err)
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
