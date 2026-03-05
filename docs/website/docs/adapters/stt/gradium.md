---
id: gradium
title: Gradiant
---

# Gradiant STT Adapter for RTP Agent

Gradiant (Gradium) provides specialized speech recognition tools focused on high accuracy and low-latency performance in complex audio environments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/gradium
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Gradiant developer documentation for acquiring the necessary API keys and tokens.

```env
GRADIUM_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Gradiant STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/gradium"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Gradiant STT adapter
	sttProvider, err := gradium.NewProvider(
		os.Getenv("GRADIUM_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize gradium adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithSTT(sttProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
