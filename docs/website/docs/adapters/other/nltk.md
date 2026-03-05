---
id: nltk
title: NLTK
---

# NLTK Other Adapter for RTP Agent

NLTK (Natural Language Toolkit) provides foundational linguistic processing tools, enabling advanced text analysis, chunking, parsing, and tokenization for your agent.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/nltk
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the NLTK Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/nltk"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the adapter
	tokenizer := nltk.NewSentenceTokenizer("en", 5, 20)

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
