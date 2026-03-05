---
id: simplismart
title: SimpliSmart
---

# SimpliSmart STT Adapter for RTP Agent

SimpliSmart provides high-performance, cost-effective infrastructure for deploying and scaling speech-to-text models like Whisper in real-time environments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/simplismart
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the SimpliSmart developer documentation for acquiring the necessary API keys and tokens.

```env
SIMPLISMART_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the SimpliSmart STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/simplismart"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the SimpliSmart STT adapter
	sttProvider, err := simplismart.NewProvider(
		os.Getenv("SIMPLISMART_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize simplismart adapter: %v", err)
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
