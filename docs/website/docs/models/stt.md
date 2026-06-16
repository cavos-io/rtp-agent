---
id: stt
title: STT
---

# STT

STT interfaces live in `core/stt`. App-level setup assigns an implementation to `agent.Agent.STT`.

Example direct constructors:

```go
deepgramSTT := deepgram.NewDeepgramSTT(apiKey, "nova-3")

openaiSTT, err := openai.NewOpenAISTT(apiKey, "gpt-4o-transcribe")
if err != nil {
	return err
}

googleSTT, err := google.NewGoogleSTT(credentialsFile)
if err != nil {
	return err
}
```

The app layer can also configure fallback STT providers through `RTP_AGENT_STT_FALLBACK_PROVIDERS`.

