---
id: aws
title: AWS
---

# AWS Realtime Adapter for RTP Agent

The AWS adapter provides seamless integration with AWS's Multimodal Realtime API, allowing your RTP Agent to process voice and text natively with ultra-low latency.

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

Below is a basic conceptual example demonstrating how to initialize the AWS Realtime adapter within an RTP Agent session:

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

	// Initialize the AWS Realtime adapter
	realtimeProvider, err := aws.NewRealtimeModel(
		os.Getenv("AWS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize AWS adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewMultimodalSession(
		agent.WithRealtimeModel(realtimeProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
