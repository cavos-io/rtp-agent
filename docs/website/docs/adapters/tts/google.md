---
id: google
title: Google
---

# Google TTS Adapter for RTP Agent

Google Cloud Text-to-Speech enables developers to synthesize natural-sounding speech with 100+ voices, available in multiple languages and variants. It applies DeepMind’s groundbreaking research in WaveNet.

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

Below is a basic conceptual example demonstrating how to initialize the Google TTS adapter within an RTP Agent session:

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

	// Initialize the Google TTS adapter
	ttsProvider, err := google.NewProvider(
		os.Getenv("GOOGLE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize google adapter: %v", err)
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
