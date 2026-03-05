---
id: durable
title: Durable
---

# Durable Other Adapter for RTP Agent

Durable provides state management and robust workflow execution capabilities, allowing agents to maintain complex, long-running conversational states reliably over time.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/durable
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Durable Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/durable"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the adapter
	workflowManager := durable.NewDurableManager()

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithWorkflow(workflowManager),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
