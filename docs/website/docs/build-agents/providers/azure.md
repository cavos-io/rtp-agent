---
id: azure
title: Azure
---

# Azure

Status: **implemented** for STT and TTS; no Azure LLM capability file exists.

Use the Azure adapter package for Azure Speech STT and TTS.

## Source-backed capabilities

- STT: `adapter/azure/stt.go`
- TTS: `adapter/azure/tts.go`

Constructors include `NewAzureSTT` and `NewAzureTTS`.

## LLM boundary

There is no `adapter/azure/llm.go` capability file. The OpenAI adapter package contains Azure OpenAI constructors for OpenAI-compatible LLM/STT/TTS paths, but that is not the same as an Azure adapter LLM capability page.

Keep Azure docs explicit about which package owns the constructor being used.

Evidence:

- `adapter/azure/stt.go`
- `adapter/azure/tts.go`
- `adapter/azure/azure_test.go`
