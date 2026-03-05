---
id: gladia
title: Gladia
---

# Gladia STT Adapter for RTP Agent

Gladia is an enterprise-grade transcription API that combines multiple speech engines to provide exceptional accuracy and speed. It supports over 99 languages and is built for real-time streaming workflows.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/gladia
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Gladia developer documentation for acquiring the necessary API keys and tokens.

```env
GLADIA_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Gladia STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/gladia"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Gladia STT adapter
	sttProvider, err := gladia.NewProvider(
		os.Getenv("GLADIA_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize gladia adapter: %v", err)
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
