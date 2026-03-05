---
id: durable
title: Durable
---

# Durable Other Adapter for RTP Agent

Durable provides state management and robust workflow execution capabilities, allowing agents to maintain complex, long-running conversational states reliably over time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/durable
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Durable developer documentation for acquiring the necessary API keys and tokens.

```env
DURABLE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Durable Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/durable"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Durable Other adapter
	pluginProvider, err := durable.NewProvider(
		os.Getenv("DURABLE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize durable adapter: %v", err)
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
