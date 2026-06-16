---
id: overview
title: Images and video
---

# Images and video

Status: **partial**.

Use this page to understand the agent-side video boundary.

`rtp-agent` does not provide a full web or mobile video application guide. It does include runtime pieces for accepting video frames and reducing how many frames are forwarded to model context.

## Sample video before sending it to models

`VoiceActivityVideoSampler` decides whether an incoming video frame should be forwarded. It samples more often while the user is speaking and less often while the user is silent. Use this to reduce model context pressure when a realtime or multimodal provider can consume image frames.

The constructor choices are:

- `NewVoiceActivityVideoSampler(session, sampleRate, opts)` for a single speaking rate plus the default silent rate.
- `NewVoiceActivityVideoSamplerWithRates(session, speakingFPS, silentFPS, opts)` when you need explicit rates for both states.

## What remains application-specific

Frontend camera capture, room publication, and UX are outside this Go docs section. Build those with LiveKit client SDKs or your product frontend, then keep this runtime focused on the agent-side frame handling.

Evidence:

- `core/agent/video_sampler.go`
- `core/agent/video_sampler_test.go`
- `library/utils/images/image.go`
- `core/llm/llm_test.go` for realtime video frame event support
