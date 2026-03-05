---
id: openai
title: OpenAI
---

# OpenAI TTS Adapter for RTP Agent

OpenAI TTS provides high-quality text-to-speech synthesis using the latest neural models. It offers six built-in voices (Alloy, Echo, Fable, Onyx, Nova, and Shimmer) optimized for different use cases.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/openai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the OpenAI developer documentation for acquiring the necessary API keys and tokens.

```env
OPENAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the OpenAI TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the OpenAI TTS adapter
	ttsProvider, err := openai.NewProvider(
		os.Getenv("OPENAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize openai adapter: %v", err)
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
