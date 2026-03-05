---
id: clova
title: Clova
---

# Clova STT Adapter for RTP Agent

Clova Speech (by NAVER) is a high-performance STT engine optimized for East Asian languages, particularly Korean. It excels in recognizing various dialects and provides high accuracy for multi-speaker environments.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/clova
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Clova developer documentation for acquiring the necessary API keys and tokens.

```env
CLOVA_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Clova STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/clova"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Clova STT adapter
	sttProvider, err := clova.NewProvider(
		os.Getenv("CLOVA_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize clova adapter: %v", err)
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
