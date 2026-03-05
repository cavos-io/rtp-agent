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

## Usage

Below is a basic conceptual example demonstrating how to initialize the BlingFire Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/blingfire"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the adapter
	tokenizer := blingfire.NewSentenceTokenizer("en", 5, 20)

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithTokenizer(tokenizer),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
