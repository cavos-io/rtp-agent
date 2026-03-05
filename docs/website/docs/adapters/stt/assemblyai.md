---
id: assemblyai
title: AssemblyAI
---

# AssemblyAI STT Adapter for RTP Agent

AssemblyAI provides state-of-the-art Speech-to-Text models that go beyond transcription, offering features like Speaker Diarization, PII Redaction, and Levenshtein-based accuracy metrics. It is ideal for enterprise-grade audio intelligence.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/assemblyai
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the AssemblyAI developer documentation for acquiring the necessary API keys and tokens.

```env
ASSEMBLYAI_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the AssemblyAI STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/assemblyai"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the AssemblyAI STT adapter
	sttProvider, err := assemblyai.NewProvider(
		os.Getenv("ASSEMBLYAI_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize assemblyai adapter: %v", err)
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
