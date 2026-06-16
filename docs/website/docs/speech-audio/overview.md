---
id: overview
title: Speech and audio
---

# Speech and audio

Speech pipelines are assembled from STT, VAD, LLM, and TTS implementations.

At app level, configure these with environment variables:

```bash
RTP_AGENT_STT_PROVIDER=deepgram
RTP_AGENT_STT_MODEL=nova-3
DEEPGRAM_API_KEY=your_key

RTP_AGENT_VAD_PROVIDER=silero

RTP_AGENT_TTS_PROVIDER=openai
RTP_AGENT_TTS_MODEL=gpt-4o-mini-tts
RTP_AGENT_TTS_VOICE=alloy
OPENAI_API_KEY=your_key
```

At package level, adapters expose concrete constructors. Examples:

```go
package main

import (
	openaiadapter "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/adapter/deepgram"
	goopenai "github.com/sashabaranov/go-openai"
)

func configureSpeech(apiKey string) error {
	sttModel := deepgram.NewDeepgramSTT(apiKey, "nova-3")

	ttsModel, err := openaiadapter.NewOpenAITTS(
		apiKey,
		goopenai.SpeechModel("gpt-4o-mini-tts"),
		goopenai.SpeechVoice("alloy"),
	)
	if err != nil {
		return err
	}

	_, _ = sttModel, ttsModel
	return nil
}
```

Use app-level configuration for normal agent execution. Use direct constructors when writing package tests, custom composition roots, or provider-specific integration code.
