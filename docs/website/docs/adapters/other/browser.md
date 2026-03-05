---
id: browser
title: Browser
---

# Browser Other Adapter for RTP Agent

The Browser adapter provides headless browser orchestration (e.g., via Playwright), enabling the RTP Agent to interact with web pages, navigate DOMs, and perform web-based actions.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/browser
```

## Authentication

Set the required environment variables in your `.env` file. Refer to the Browser developer documentation for acquiring the necessary API keys and tokens.

```env
BROWSER_API_KEY=your_api_key_here
```

## Usage

Below is a basic conceptual example demonstrating how to initialize the Browser Other adapter within an RTP Agent session:

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	// Initialize the Browser Other adapter
	pluginProvider, err := browser.NewProvider(
		os.Getenv("BROWSER_API_KEY"),
	)
	if err != nil {
		log.Fatalf("failed to initialize browser adapter: %v", err)
	}

	// Create and configure the RTP agent session
	session := agent.NewSession(
		agent.WithPlugin(pluginProvider),
	)

	// Start the session
	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```
