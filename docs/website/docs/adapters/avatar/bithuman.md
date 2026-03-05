---
id: bithuman
title: BitHuman
---

# BitHuman Avatar Adapter for RTP Agent

BitHuman provides AI-driven digital humans that combine advanced visual synthesis with interactive capabilities for various industry use cases.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/bithuman
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the BitHuman developer documentation for acquiring the necessary API keys and tokens.

```env
BITHUMAN_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the BitHuman Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/bithuman"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the BitHuman Avatar adapter
	avatarProvider, err := bithuman.NewProvider(
		os.Getenv("BITHUMAN_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize bithuman adapter: %v", err)
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
