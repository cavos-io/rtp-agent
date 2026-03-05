---
id: silero
title: Silero
---

# Silero Other Adapter for RTP Agent

Silero provides an ultra-fast, robust Voice Activity Detection (VAD) model. This is an essential component for determining exactly when a user starts or stops speaking, enabling accurate turn-taking.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/silero
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Silero developer documentation for acquiring the necessary API keys and tokens.

```env
SILERO_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Silero Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Silero Other adapter
	pluginProvider, err := silero.NewProvider(
		os.Getenv("SILERO_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize silero adapter: %v", err)
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
