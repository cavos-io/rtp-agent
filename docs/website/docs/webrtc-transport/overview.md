---
id: overview
title: WebRTC Transport
---

# WebRTC Transport

Status: **partial**.

Use this page to understand the transport boundary for the agent process.

The worker uses LiveKit room APIs to join rooms and exchange realtime media/data. That is the agent-side WebRTC path. This repository does not document browser WebRTC capture, mobile UI, or frontend transport tuning.

## Agent-side transport

The source-backed path is:

1. `worker.AgentServer` accepts a job.
2. `JobContext` connects to a LiveKit room.
3. `RoomIO` subscribes to room input and publishes agent output.
4. `AgentSession` consumes audio/text/video events and schedules responses.

Agora transport code is also present under `interface/worker/agora`, but it should be documented as a transport option only where source and tests cover the exact behavior.

## Frontend transport

A web or mobile client should use LiveKit client SDKs outside this repository. Keep client network, permission, and UX guidance in frontend-specific docs once a frontend example exists.

Evidence:

- `interface/worker/room_io.go`
- `interface/worker/transport.go`
- `interface/worker/agora`
- `go.mod` dependencies on LiveKit Server SDK and Pion WebRTC
