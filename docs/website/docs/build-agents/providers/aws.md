---
id: aws
title: AWS
---

# AWS

Status: **implemented** for LLM, STT, and TTS.

Evidence:

- `adapter/aws/llm.go`
- `adapter/aws/stt.go`
- `adapter/aws/tts.go`
- `adapter/aws/*_test.go`

Constructors include `NewAWSLLM`, `NewAWSSTT`, and `NewAWSTTS`. AWS region is configured through `RTP_AGENT_AWS_REGION` or `AWS_REGION`.

