---
id: overview
title: Agent Frontends
---

# Agent Frontends

Status: **planned/not implemented** as a dedicated docs/product surface.

Use this page to understand the boundary between a user-facing app and the Go agent runtime.

LiveKit's Agents docs include frontend guides for connecting web and mobile clients to rooms. `rtp-agent` does not currently include a frontend SDK package, embed widget, or checked-in frontend application. The Go source is the agent-side runtime: it starts workers, joins rooms, processes room I/O, and publishes agent output.

## What to build outside this repository

Build user-facing clients with LiveKit client SDKs or your application framework. That client should:

- connect the user to the LiveKit room
- publish microphone, camera, screen, or text input as your product requires
- render remote audio/video/data from the room
- handle product-specific authentication and UI

## What this repository handles

The agent side uses `interface/worker` to accept jobs and join rooms. Room input and output are handled by `RoomIO` and the active `AgentSession`.

Document a frontend guide in this repository only after there is a source-backed example or package to maintain.

Evidence:

- No frontend SDK package exists under `core`, `interface`, or `adapter`.
- Room and worker runtime code exists in `interface/worker/room_io.go` and `interface/worker/server.go`.
