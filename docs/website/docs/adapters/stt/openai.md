---
id: openai
title: OpenAI
---

# OpenAI STT Adapter for RTP Agent

OpenAI's Whisper model is a general-purpose speech recognition model. It is trained on a large dataset of diverse audio and is also a multitasking model that can perform multilingual speech recognition as well as speech translation.

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

Below is a basic conceptual example demonstrating how to initialize the OpenAI STT adapter within an RTP Agent session:

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

	// Initialize the OpenAI STT adapter
	sttProvider, err := openai.NewProvider(
		os.Getenv("OPENAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize openai adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithSTT(sttProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
