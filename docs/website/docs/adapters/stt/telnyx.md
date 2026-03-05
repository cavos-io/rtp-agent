---
id: telnyx
title: Telnyx
---

# Telnyx STT Adapter for RTP Agent

Telnyx STT offers real-time transcription services integrated directly into their global communications network, providing low-latency audio processing for telephony and VOIP applications.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/telnyx
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Telnyx developer documentation for acquiring the necessary API keys and tokens.

```env
TELNYX_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Telnyx STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/telnyx"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Telnyx STT adapter
	sttProvider, err := telnyx.NewProvider(
		os.Getenv("TELNYX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize telnyx adapter: %v", err)
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
