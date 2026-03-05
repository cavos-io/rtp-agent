---
id: tavus
title: Tavus
---

# Tavus Avatar Adapter for RTP Agent

Tavus specializes in high-fidelity, personalized video generation, allowing for the creation of unique, lip-synced avatars that look and sound just like real people.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/tavus
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Tavus developer documentation for acquiring the necessary API keys and tokens.

```env
TAVUS_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Tavus Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/tavus"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Tavus Avatar adapter
	avatarProvider, err := tavus.NewProvider(
		os.Getenv("TAVUS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize tavus adapter: %v", err)
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
