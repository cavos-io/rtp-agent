---
id: speechmatics
title: Speechmatics
---

# Speechmatics STT Adapter for RTP Agent

Speechmatics is a global leader in autonomous speech recognition. Their 'Ursa' model supports a vast array of languages and dialects with high accuracy, even in noisy environments or with complex accents.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/speechmatics
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Speechmatics developer documentation for acquiring the necessary API keys and tokens.

```env
SPEECHMATICS_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Speechmatics STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/speechmatics"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Speechmatics STT adapter
	sttProvider, err := speechmatics.NewProvider(
		os.Getenv("SPEECHMATICS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize speechmatics adapter: %v", err)
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
