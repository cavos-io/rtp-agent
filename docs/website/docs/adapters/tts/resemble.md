---
id: resemble
title: Resemble
---

# Resemble TTS Adapter for RTP Agent

Resemble AI focuses on high-quality voice cloning and real-time synthesis. Their platform allows for deep customization of voices, including emotional control and cross-lingual synthesis for global applications.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/resemble
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Resemble developer documentation for acquiring the necessary API keys and tokens.

```env
RESEMBLE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Resemble TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/resemble"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Resemble TTS adapter
	ttsProvider, err := resemble.NewProvider(
		os.Getenv("RESEMBLE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize resemble adapter: %v", err)
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
