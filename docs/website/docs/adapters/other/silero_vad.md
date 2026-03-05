---
id: silero_vad
title: Silero_vad
---

# Silero_vad Other Adapter for RTP Agent

Silero_vad provides seamless integration with Silero_vad's specialized tools and utilities, allowing your RTP Agent to handle complex auxiliary tasks.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/silero_vad
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Silero_vad developer documentation for acquiring the necessary API keys and tokens.

```env
SILERO_VAD_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Silero_vad Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/silero_vad"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Silero_vad Other adapter
	pluginProvider, err := silero_vad.NewProvider(
		os.Getenv("SILERO_VAD_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize silero_vad adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithPlugin(pluginProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
