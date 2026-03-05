---
id: upliftai
title: Uplift AI
---

# Uplift AI TTS Adapter for RTP Agent

Uplift AI provides voice synthesis solutions focused on delivering uplifting and engaging conversational experiences for users.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/upliftai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Uplift AI developer documentation for acquiring the necessary API keys and tokens.

```env
UPLIFTAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Uplift AI TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/upliftai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Uplift AI TTS adapter
	ttsProvider, err := upliftai.NewProvider(
		os.Getenv("UPLIFTAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize upliftai adapter: %v", err)
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
