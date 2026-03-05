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

## Authentication

Set the required environment variables in your `.env` file. Refer to the NLTK developer documentation for acquiring the necessary API keys and tokens.

```env
NLTK_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the NLTK Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/nltk"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the NLTK Other adapter
	pluginProvider, err := nltk.NewProvider(
		os.Getenv("NLTK_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize nltk adapter: %v", err)
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
