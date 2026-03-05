---
id: avatario
title: Avatar.io
---

# Avatar.io Avatar Adapter for RTP Agent

Avatar.io provides specialized interactive avatar capabilities, integrating 3D and 2D models for dynamic and visually engaging virtual presence.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/avatario
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Avatar.io developer documentation for acquiring the necessary API keys and tokens.

```env
AVATARIO_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Avatar.io Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/avatario"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Avatar.io Avatar adapter
	avatarProvider, err := avatario.NewProvider(
		os.Getenv("AVATARIO_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize avatario adapter: %v", err)
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
