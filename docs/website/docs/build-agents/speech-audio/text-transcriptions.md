---
id: text-transcriptions
title: Text and transcriptions
---

# Text and transcriptions

Status: **partial**.

`rtp-agent` includes transcription synchronization and room text handling. Behavior is implemented through agent transcription helpers and `RoomIO`.

Evidence:

- `core/agent/transcription.go`
- `core/agent/transcription_test.go`
- `interface/worker/room_io.go`
- `interface/worker/room_io_test.go`

Use STT adapters for audio-to-text and `RoomIO` for room text input/output. Transcription details are provider-specific and should be checked against adapter tests.

