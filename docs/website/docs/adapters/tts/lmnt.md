---
id: lmnt
title: LMNT
---

# LMNT TTS Adapter for RTP Agent

LMNT is a high-fidelity speech synthesis engine built for developers. It offers a variety of expressive voices with low latency, ensuring that your RTP Agent sounds professional and responsive.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/lmnt
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the LMNT developer documentation for acquiring the necessary API keys and tokens.

```env
LMNT_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the LMNT TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/lmnt"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the LMNT TTS adapter
	ttsProvider, err := lmnt.NewProvider(
		os.Getenv("LMNT_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize lmnt adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithTTS(ttsProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
