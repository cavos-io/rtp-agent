---
id: overview
title: WebRTC Transport
---

# WebRTC Transport

Status: **partial**.

Evidence:

- `interface/worker/room_io.go`
- `interface/worker/transport.go`
- `interface/worker/agora`
- `go.mod` dependencies on LiveKit Server SDK and Pion WebRTC

The worker joins rooms and publishes/subscribes media through room I/O. Agora transport is also present. This is not a full frontend WebRTC guide.

