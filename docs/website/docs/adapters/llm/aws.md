---
id: aws
title: AWS
---

# AWS LLM Adapter for RTP Agent

Amazon Bedrock provides a unified API to access foundation models from Amazon, Anthropic, Meta, Mistral, and more, integrated into the AWS ecosystem.

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

Below is a basic conceptual example demonstrating how to initialize the AWS LLM adapter within an RTP Agent session:

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

	// Initialize the AWS LLM adapter
	llmProvider, err := aws.NewProvider(
		os.Getenv("AWS_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize aws adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithLLM(llmProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
