---
id: text-transcriptions
title: Text and transcriptions
---

# Text and transcriptions

Status: **partial**.

Use STT for user speech, TTS for agent speech, and transcription helpers when text needs to stay aligned with audio playback.

The runtime has two separate concerns:

- Text input and output through room I/O and session methods.
- Transcript timing for spoken output through `TranscriptSynchronizer`.

`TranscriptSynchronizer` accepts text and audio frames, then emits text chunks according to played audio duration. When speech is interrupted, it flushes remaining buffered text and stops syncing. That behavior is covered by `core/agent/transcription_test.go`.

## Where to configure speech

Choose STT and TTS providers with `AppConfig` fields or environment variables such as `RTP_AGENT_STT_PROVIDER`, `RTP_AGENT_STT_MODEL`, `RTP_AGENT_TTS_PROVIDER`, and `RTP_AGENT_TTS_MODEL`.

Use provider pages when you need provider-specific options. Transcription shape, timestamps, and interim results are adapter-specific, so check adapter source and tests before documenting exact behavior for a provider.

Evidence:

- `core/agent/transcription.go`
- `core/agent/transcription_test.go`
- `interface/worker/room_io.go`
- `interface/worker/room_io_test.go`
