---
id: google
title: Google
---

# Google STT Adapter for RTP Agent

Google Cloud Speech-to-Text enables developers to convert audio to text by applying powerful neural network models. It supports a wide range of languages and is highly scalable for real-time streaming applications.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/google
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Google developer documentation for acquiring the necessary API keys and tokens.

```env
GOOGLE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Google STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/google"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Google STT adapter
	sttProvider, err := google.NewProvider(
		os.Getenv("GOOGLE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize google adapter: %v", err)
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
