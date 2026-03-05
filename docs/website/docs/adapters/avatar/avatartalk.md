---
id: avatartalk
title: AvatarTalk
---

# AvatarTalk Avatar Adapter for RTP Agent

AvatarTalk focuses on synchronizing voice and avatar animations to create lifelike conversational experiences and natural expressions.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/avatartalk
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the AvatarTalk developer documentation for acquiring the necessary API keys and tokens.

```env
AVATARTALK_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the AvatarTalk Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/avatartalk"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the AvatarTalk Avatar adapter
	avatarProvider, err := avatartalk.NewProvider(
		os.Getenv("AVATARTALK_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize avatartalk adapter: %v", err)
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
