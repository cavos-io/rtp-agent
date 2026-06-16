---
id: overview
title: Manage and deploy
---

# Manage and deploy

Status: **partial**.

Use this page to prepare an `rtp-agent` process for deployment.

The source-backed deployment unit is a Go binary that creates an `app.App`, configures its `worker.AgentServer`, and runs the server through `interface/cli.RunApp` or equivalent process code. Platform-specific recipes are intentionally deferred until they are backed by source, CI, or deployment examples.

## Deployment shape

1. Build a Go entrypoint such as `cmd/main.go` or `examples/voice_agents/basic_agent/main.go`.
2. Configure LiveKit credentials and provider settings through environment variables.
3. Run the binary in `start` mode for worker operation.
4. Use drain and shutdown settings from `worker.WorkerOptions` for graceful termination.
5. Use telemetry environment variables when logs or evaluation data must be exported.

## Required configuration

At minimum, a LiveKit worker needs:

- `LIVEKIT_URL`
- `LIVEKIT_API_KEY`
- `LIVEKIT_API_SECRET`

Most real deployments also set model providers such as `RTP_AGENT_LLM_PROVIDER`, `RTP_AGENT_STT_PROVIDER`, and `RTP_AGENT_TTS_PROVIDER`, plus provider-specific API keys.

## What is deferred

Kubernetes manifests, LiveKit Cloud deployment flows, systemd units, container images, and cloud-platform recipes should be added only when the repository has tested examples or CI-backed instructions.

Evidence:

- `cmd/main.go`
- `app/app.go`
- `interface/cli/cli.go`
- `interface/worker/server.go`
