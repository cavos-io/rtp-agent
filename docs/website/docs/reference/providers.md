---
id: providers
title: Provider capabilities
---

# Provider capabilities

This table is generated from source layout at `v0.0.67`. A check mark means the adapter package contains the corresponding capability file.

| Provider | LLM | STT | TTS | Realtime | Avatar | VAD |
|---|---:|---:|---:|---:|---:|---:|
| anthropic | yes |  |  |  |  |  |
| assemblyai |  | yes |  |  |  |  |
| asyncai |  |  | yes |  |  |  |
| aws | yes | yes | yes |  |  |  |
| azure |  | yes | yes |  |  |  |
| baseten | yes | yes | yes |  |  |  |
| bey |  |  |  |  | yes |  |
| bithuman |  |  |  |  | yes |  |
| cambai |  |  | yes |  |  |  |
| cartesia |  | yes | yes |  |  |  |
| cavos |  | yes | yes |  |  |  |
| cerebras | yes |  |  |  |  |  |
| clova |  | yes | yes |  |  |  |
| deepgram |  | yes | yes |  |  |  |
| did |  |  |  |  | yes |  |
| elevenlabs |  | yes | yes |  |  |  |
| fal | yes | yes |  |  |  |  |
| fireworksai | yes | yes |  |  |  |  |
| fishaudio |  |  | yes |  |  |  |
| gladia |  | yes |  |  |  |  |
| gnani |  | yes | yes |  |  |  |
| google | yes | yes | yes |  |  |  |
| gradium | yes | yes | yes |  |  |  |
| groq | yes | yes | yes |  |  |  |
| hedra | yes |  |  |  | yes |  |
| hume | yes |  | yes |  |  |  |
| inworld | yes | yes | yes |  |  |  |
| langchain | yes |  |  |  |  |  |
| lemonslice | yes |  |  |  | yes |  |
| livekit | yes | yes | yes |  |  |  |
| lmnt |  |  | yes |  |  |  |
| minimal | yes |  |  |  |  |  |
| minimax | yes |  | yes |  |  |  |
| mistralai | yes | yes | yes |  |  |  |
| murf |  |  | yes |  |  |  |
| neuphonic |  |  | yes |  |  |  |
| nvidia | yes |  | yes |  |  |  |
| openai | yes | yes | yes | yes |  |  |
| perplexity | yes |  |  |  |  |  |
| phonic |  |  |  | yes |  |  |
| resemble |  |  | yes |  |  |  |
| respeecher |  |  | yes |  |  |  |
| rime |  |  | yes |  |  |  |
| rtzr |  | yes |  |  |  |  |
| runway |  |  |  |  | yes |  |
| sarvam | yes |  | yes |  |  |  |
| silero |  |  |  |  |  | yes |
| simli | yes |  |  |  | yes |  |
| simplismart | yes | yes | yes |  |  |  |
| smallestai | yes | yes | yes |  |  |  |
| soniox |  | yes | yes |  |  |  |
| speechify |  |  | yes |  |  |  |
| speechmatics |  | yes | yes |  |  |  |
| spitch |  | yes | yes |  |  |  |
| tavus |  |  |  |  | yes |  |
| telnyx | yes | yes | yes |  |  |  |
| ten |  |  |  |  |  | yes |
| trugen | yes |  |  |  | yes |  |
| ultravox |  |  | yes |  |  |  |
| upliftai | yes |  | yes |  |  |  |
| xai | yes | yes | yes |  |  |  |

## Constructor examples

Source-backed constructors include:

- `openai.NewOpenAILLM`, `openai.NewOpenAISTT`, `openai.NewOpenAITTS`, `openai.NewRealtimeModel`
- `deepgram.NewDeepgramSTT`, `deepgram.NewDeepgramTTS`
- `google.NewGoogleLLM`, `google.NewGoogleSTT`, `google.NewGoogleTTS`
- `aws.NewAWSLLM`, `aws.NewAWSSTT`, `aws.NewAWSTTS`
- `anthropic.NewAnthropicLLM`
- `livekit.NewLiveKitInferenceLLM`, `livekit.NewSTT`, `livekit.NewTTS`
- `silero.NewSileroVAD`, `ten.NewVAD`

There is no generic `NewProvider` constructor pattern in source.

