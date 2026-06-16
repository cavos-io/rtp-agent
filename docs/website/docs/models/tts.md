---
id: tts
title: TTS
---

# TTS

TTS interfaces live in `core/tts`. App-level setup assigns an implementation to `agent.Agent.TTS`.

Example direct constructors:

```go
package main

import (
	"github.com/cavos-io/rtp-agent/adapter/deepgram"
	"github.com/cavos-io/rtp-agent/adapter/google"
	openaiadapter "github.com/cavos-io/rtp-agent/adapter/openai"
	goopenai "github.com/sashabaranov/go-openai"
)

func configureTTS(apiKey string, credentialsFile string) error {
	deepgramTTS := deepgram.NewDeepgramTTS(apiKey, "aura-2-thalia-en")

	openaiTTS, err := openaiadapter.NewOpenAITTS(
		apiKey,
		goopenai.SpeechModel("gpt-4o-mini-tts"),
		goopenai.SpeechVoice("alloy"),
	)
	if err != nil {
		return err
	}

	googleTTS, err := google.NewGoogleTTS(credentialsFile, google.WithGoogleTTSVoice("en-US-Chirp3-HD-Aoede"))
	if err != nil {
		return err
	}

	_, _, _ = deepgramTTS, openaiTTS, googleTTS
	return nil
}
```

The session can apply text replacements, transforms, and stream pacing through `AgentSessionOptions` or the matching `RTP_AGENT_TTS_*` environment variables.
