---
id: rtzr
title: RTZR
---

# RTZR STT Adapter for RTP Agent

RTZR (ReturnZero) is a leading Korean STT specialist. Their models are fine-tuned for the nuances of the Korean language, making them the preferred choice for Korean-language real-time agents.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/rtzr
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the RTZR developer documentation for acquiring the necessary API keys and tokens.

```env
RTZR_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the RTZR STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/rtzr"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the RTZR STT adapter
	sttProvider, err := rtzr.NewProvider(
		os.Getenv("RTZR_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize rtzr adapter: %v", err)
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
