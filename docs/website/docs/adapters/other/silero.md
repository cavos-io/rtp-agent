---
id: silero
title: Silero
---

# Silero Other Adapter for RTP Agent

Silero provides a Voice Activity Detection (VAD) adapter for turn-taking.

The Go adapter exposes Silero-compatible options and metadata through
`adapter/silero`. It currently uses the built-in `core/vad.SimpleVAD` engine as
the runtime fallback, so it works without CGO or ONNX runtime dependencies in
the base install. Native ONNX-backed Silero inference can be added behind the
same package later.

## Installation

Add the adapter to your Go project:

```bash
go get github.com/cavos-io/rtp-agent/adapter/silero
```

## Usage

Initialize the VAD adapter and pass it to an agent session:

```go
package main

import (
	"context"
	"log"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/agent"
)

func main() {
	ctx := context.Background()

	vadProvider := silero.NewSileroVAD(
		silero.WithMinSpeechDuration(0.05),
		silero.WithMinSilenceDuration(0.55),
		silero.WithActivationThreshold(0.5),
	)

	session := agent.NewSession(
		agent.WithVAD(vadProvider),
	)

	if err := session.Start(ctx); err != nil {
		log.Fatalf("session failed: %v", err)
	}
}
```

## Options

Common options:

- `WithMinSpeechDuration`
- `WithMinSilenceDuration`
- `WithPrefixPaddingDuration`
- `WithMaxBufferedSpeech`
- `WithActivationThreshold`
- `WithDeactivationThreshold`
- `WithSampleRate`

Supported sample rates are 8 kHz and 16 kHz.
