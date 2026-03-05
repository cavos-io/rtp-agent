---
id: azure
title: Azure
---

# Azure TTS Adapter for RTP Agent

Azure provides access to Microsoft's comprehensive suite of Cognitive Services, allowing RTP Agents to utilize powerful, enterprise-grade AI features and custom speech models.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/azure
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Azure developer documentation for acquiring the necessary API keys and tokens.

```env
AZURE_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Azure TTS adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/azure"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Azure TTS adapter
	ttsProvider, err := azure.NewProvider(
		os.Getenv("AZURE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize azure adapter: %v", err)
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
