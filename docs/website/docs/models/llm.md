---
id: llm
title: LLM
---

# LLM

LLM interfaces and chat types live in `core/llm`. App-level setup assigns an implementation to `agent.Agent.LLM`.

Example direct constructors:

```go
openaiLLM, err := openai.NewOpenAILLM(apiKey, "gpt-4.1-mini")
if err != nil {
	return err
}

anthropicLLM, err := anthropic.NewAnthropicLLM(apiKey, "claude-sonnet-4-5")
if err != nil {
	return err
}

googleLLM, err := google.NewGoogleLLM(apiKey, "gemini-2.5-flash")
if err != nil {
	return err
}
```

For normal applications, prefer `AppConfig` so the app layer can also wire STT, TTS, VAD, realtime, avatar, tools, and worker settings.

