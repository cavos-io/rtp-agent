---
id: simli
title: Simli
---

# Simli Avatar Adapter for RTP Agent

Simli provides ultra-low latency digital humans designed for real-time interaction. It ensures perfect lip-sync and fluid movement over WebRTC.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/simli
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Simli developer documentation for acquiring the necessary API keys and tokens.

```env
SIMLI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Simli Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/simli"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Simli Avatar adapter
	avatarProvider, err := simli.NewProvider(
		os.Getenv("SIMLI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize simli adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithAvatar(avatarProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
