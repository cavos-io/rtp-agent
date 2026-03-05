---
id: anam
title: Anam
---

# Anam Avatar Adapter for RTP Agent

Anam is a platform for creating interactive AI personas. It provides specialized capabilities for generating realistic digital personas and orchestrating their behavior.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/anam
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Anam developer documentation for acquiring the necessary API keys and tokens.

```env
ANAM_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Anam Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/anam"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Anam Avatar adapter
	avatarProvider, err := anam.NewProvider(
		os.Getenv("ANAM_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize anam adapter: %v", err)
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
