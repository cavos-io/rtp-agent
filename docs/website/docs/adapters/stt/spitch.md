---
id: spitch
title: Spitch
---

# Spitch STT Adapter for RTP Agent

Spitch is a Swiss-based company specializing in speech technology. Their STT solutions are highly optimized for European languages and dialects, focusing on precision in customer service and contact center automation.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/spitch
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Spitch developer documentation for acquiring the necessary API keys and tokens.

```env
SPITCH_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Spitch STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/spitch"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Spitch STT adapter
	sttProvider, err := spitch.NewProvider(
		os.Getenv("SPITCH_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize spitch adapter: %v", err)
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
