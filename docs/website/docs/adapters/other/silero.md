---
id: silero
title: Silero
---

# Silero Other Adapter for RTP Agent

Silero provides an ultra-fast, robust Voice Activity Detection (VAD) model. This is an essential component for determining exactly when a user starts or stops speaking, enabling accurate turn-taking.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/silero
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Silero Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the adapter
	vadProvider := silero.NewSileroVAD(silero.VADOption{})

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithVAD(vadProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
