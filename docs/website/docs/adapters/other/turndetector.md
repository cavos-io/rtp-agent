---
id: turndetector
title: Turn Detector
---

# Turn Detector Other Adapter for RTP Agent

Turn Detector utilizes specialized machine learning models to analyze acoustic conversational cues. It intelligently predicts end-of-turn scenarios to vastly improve agent responsiveness.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/turndetector
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Turn Detector Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/turndetector"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the adapter
	turnDetector := turndetector.NewEOUPredictor("default_model")

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithTurnDetector(turnDetector),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
