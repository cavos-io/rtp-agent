---
id: configuration
title: Configuration reference
---

# Configuration reference

`app.DefaultConfigFromEnv()` reads environment variables into `app.AppConfig`.

## Model selection

| Field | Environment variable |
|---|---|
| `Instructions` | `RTP_AGENT_INSTRUCTIONS` |
| `LLMProvider` | `RTP_AGENT_LLM_PROVIDER` |
| `LLMModel` | `RTP_AGENT_LLM_MODEL` |
| `LLMBaseURL` | `RTP_AGENT_LLM_BASE_URL` |
| `STTProvider` | `RTP_AGENT_STT_PROVIDER` |
| `STTModel` | `RTP_AGENT_STT_MODEL` |
| `STTLanguage` | `RTP_AGENT_STT_LANGUAGE` |
| `VADProvider` | `RTP_AGENT_VAD_PROVIDER` |
| `TTSProvider` | `RTP_AGENT_TTS_PROVIDER` |
| `TTSModel` | `RTP_AGENT_TTS_MODEL` |
| `TTSVoice` | `RTP_AGENT_TTS_VOICE` |
| `RealtimeProvider` | `RTP_AGENT_REALTIME_PROVIDER` |
| `RealtimeModel` | `RTP_AGENT_REALTIME_MODEL` |
| `AvatarProvider` | `RTP_AGENT_AVATAR_PROVIDER` |
| `TurnDetectorProvider` | `RTP_AGENT_TURN_DETECTOR_PROVIDER` |

## LiveKit and transport

| Field | Environment variable |
|---|---|
| LiveKit API key | `LIVEKIT_API_KEY` |
| LiveKit API secret | `LIVEKIT_API_SECRET` |
| Worker transport | `RTP_AGENT_TRANSPORT` |
| Agora app ID | `AGORA_APP_ID` |
| Agora app certificate | `AGORA_APP_CERTIFICATE` |
| Agora channel | `AGORA_CHANNEL` |
| Agora UID | `AGORA_UID` |
| Agora token | `AGORA_TOKEN` |

## Provider credentials

Provider credentials are read into `AppConfig` fields. Common examples:

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `DEEPGRAM_API_KEY`
- `GOOGLE_API_KEY`
- `GOOGLE_APPLICATION_CREDENTIALS`
- `AWS_REGION`
- `ELEVENLABS_API_KEY`
- `GROQ_API_KEY`
- `XAI_API_KEY`
- `LIVEKIT_API_KEY`
- `LIVEKIT_API_SECRET`

Check `app.DefaultConfigFromEnv` for the complete list at this version.

