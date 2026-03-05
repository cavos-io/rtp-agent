---
id: liveavatar
title: LiveAvatar
---

# LiveAvatar Avatar Adapter for RTP Agent

LiveAvatar offers dynamic, interactive avatar solutions that can be easily integrated into real-time streaming environments for engaging user experiences.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/liveavatar
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the LiveAvatar developer documentation for acquiring the necessary API keys and tokens.

```env
LIVEAVATAR_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the LiveAvatar Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/liveavatar"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the LiveAvatar Avatar adapter
	avatarProvider, err := liveavatar.NewProvider(
		os.Getenv("LIVEAVATAR_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize liveavatar adapter: %v", err)
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
