---
id: blingfire
title: BlingFire
---

# BlingFire Other Adapter for RTP Agent

BlingFire is a lightning-fast tokenization library built by Microsoft. It is highly optimized for performance and is used for advanced natural language processing pipelines.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/blingfire
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the BlingFire developer documentation for acquiring the necessary API keys and tokens.

```env
BLINGFIRE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the BlingFire Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/blingfire"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the BlingFire Other adapter
	pluginProvider, err := blingfire.NewProvider(
		os.Getenv("BLINGFIRE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize blingfire adapter: %v", err)
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
