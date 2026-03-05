---
id: bey
title: Bey
---

# Bey Avatar Adapter for RTP Agent

Bey provides specialized avatar animation technologies that focus on natural movement and high-fidelity visual rendering for real-time agents.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/bey
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Bey developer documentation for acquiring the necessary API keys and tokens.

```env
BEY_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Bey Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/bey"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Bey Avatar adapter
	avatarProvider, err := bey.NewProvider(
		os.Getenv("BEY_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize bey adapter: %v", err)
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
