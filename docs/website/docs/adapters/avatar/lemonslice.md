---
id: lemonslice
title: LemonSlice
---

# LemonSlice Avatar Adapter for RTP Agent

LemonSlice provides tools for generating and animating digital personas, enabling rich visual interactions within conversational agent workflows.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/lemonslice
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the LemonSlice developer documentation for acquiring the necessary API keys and tokens.

```env
LEMONSLICE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the LemonSlice Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/lemonslice"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the LemonSlice Avatar adapter
	avatarProvider, err := lemonslice.NewProvider(
		os.Getenv("LEMONSLICE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize lemonslice adapter: %v", err)
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
