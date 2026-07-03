---
id: aws
title: AWS
---

# AWS

Status: **implemented** for LLM, STT, TTS, and realtime.

Use AWS when you want Bedrock-style LLM support, Transcribe streaming, Polly synthesis, or Nova Sonic realtime voice through the source-backed adapter package.

## Source-backed capabilities

- LLM: `adapter/aws/llm.go`
- STT: `adapter/aws/stt.go`
- TTS: `adapter/aws/tts.go`
- Realtime: `adapter/aws/realtime.go`

Constructors include `NewAWSLLM`, `NewAWSSTT`, `NewAWSTTS`, `NewAWSRealtimeModel`, `NewAWSRealtimeModelWithNovaSonic1`, and `NewAWSRealtimeModelWithNovaSonic2`.

## Region configuration

AWS region can come from constructor arguments or app configuration. `app.DefaultConfigFromEnv()` reads `RTP_AGENT_AWS_REGION` and then `AWS_REGION`.

Do not document AWS avatar support unless matching capability files are added.

Evidence:

- `adapter/aws/llm.go`
- `adapter/aws/stt.go`
- `adapter/aws/tts.go`
- `adapter/aws/realtime.go`
- `adapter/aws/*_test.go`
