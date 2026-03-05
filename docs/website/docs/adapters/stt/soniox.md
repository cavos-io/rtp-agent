---
id: soniox
title: Soniox
---

# Soniox STT Adapter for RTP Agent

Soniox is an advanced speech-to-text platform that leverages proprietary deep learning models to achieve industry-leading accuracy. It is designed for low-latency, real-time audio processing in enterprise environments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/soniox
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Soniox developer documentation for acquiring the necessary API keys and tokens.

```env
SONIOX_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Soniox STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/soniox"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Soniox STT adapter
	sttProvider, err := soniox.NewProvider(
		os.Getenv("SONIOX_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize soniox adapter: %v", err)
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
