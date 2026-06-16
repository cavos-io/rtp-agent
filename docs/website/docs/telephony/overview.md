---
id: overview
title: Telephony
---

# Telephony

Status: **partial**.

Use this page when your agent needs phone-call style behavior, such as DTMF input, ending a session, IVR detection, or warm transfer.

`rtp-agent` has telephony-adjacent runtime pieces, but it does not include a full SIP provisioning guide or hosted telephony product workflow. Treat SIP trunks, phone numbers, carrier configuration, and inbound/outbound call setup as external LiveKit or provider configuration until this repository adds source-backed guides for them.

## Source-backed pieces

- `core/beta/tools/send_dtmf.go` exposes a DTMF tool.
- `core/beta/tools/end_call.go` exposes a session-end tool.
- `core/beta/workflows/warm_transfer.go` implements a warm-transfer workflow helper.
- `core/agent/ivr.go` contains IVR behavior hooks.
- `adapter/telnyx` provides LLM, STT, and TTS provider adapters, not a complete telephony control plane.

## How to use this safely

Use the beta tools and workflow helpers only when their current tests match your scenario. Keep provider/SIP setup out of these docs unless the repo contains a tested example for that exact flow.

Evidence:

- `core/beta/tools/send_dtmf.go`
- `core/beta/tools/end_call.go`
- `core/beta/workflows/warm_transfer.go`
- `core/agent/ivr.go`
- `app/app.go`
- `adapter/telnyx`
