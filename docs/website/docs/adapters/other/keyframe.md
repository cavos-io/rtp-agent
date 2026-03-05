---
id: keyframe
title: Keyframe
---

# Keyframe Other Adapter for RTP Agent

Keyframe specializes in audio-driven animation, providing precise synchronization between generated speech, lip-syncs, and broader visual timelines.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/keyframe
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Keyframe developer documentation for acquiring the necessary API keys and tokens.

```env
KEYFRAME_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Keyframe Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/keyframe"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Keyframe Other adapter
	pluginProvider, err := keyframe.NewProvider(
		os.Getenv("KEYFRAME_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize keyframe adapter: %v", err)
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
