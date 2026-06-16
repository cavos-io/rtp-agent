---
id: azure
title: Azure
---

# Azure

Status: **implemented** for STT and TTS; no Azure LLM capability file exists.

Evidence:

- `adapter/azure/stt.go`
- `adapter/azure/tts.go`
- `adapter/azure/azure_test.go`

Constructors include `NewAzureSTT` and `NewAzureTTS`. Do not document Azure LLM as available unless a source-backed `llm.go` is added.

