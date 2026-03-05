---
id: aws
title: AWS
---

# AWS STT Adapter for RTP Agent

Amazon Transcribe uses advanced machine learning to provide high-quality speech-to-text capabilities. It is fully integrated into the AWS ecosystem and optimized for real-time transcription of phone calls and meetings.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/aws
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the AWS developer documentation for acquiring the necessary API keys and tokens.

```env
AWS_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the AWS STT adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/aws"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the AWS STT adapter
	sttProvider, err := aws.NewProvider(
		os.Getenv("AWS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize aws adapter: %v", err)
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
