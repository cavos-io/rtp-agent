---
id: trugen
title: TruGen
---

# TruGen Avatar Adapter for RTP Agent

TruGen provides robust avatar synthesis capabilities, focusing on delivering high-quality visual representations for professional and enterprise applications.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/trugen
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the TruGen developer documentation for acquiring the necessary API keys and tokens.

```env
TRUGEN_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the TruGen Avatar adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/trugen"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the TruGen Avatar adapter
	avatarProvider, err := trugen.NewProvider(
		os.Getenv("TRUGEN_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize trugen adapter: %v", err)
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
