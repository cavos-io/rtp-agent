---
id: aws
title: AWS
---

# AWS TTS Adapter for RTP Agent

Amazon Polly is a service that turns text into lifelike speech, allowing you to create applications that talk, and build entirely new categories of speech-enabled products.

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

Below is a basic conceptual example demonstrating how to initialize the AWS TTS adapter within an RTP Agent session:

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

	// Initialize the AWS TTS adapter
	ttsProvider, err := aws.NewProvider(
		os.Getenv("AWS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize aws adapter: %v", err)
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
