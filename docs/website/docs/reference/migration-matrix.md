---
id: migration-matrix
title: LiveKit IA migration matrix
---

# LiveKit IA migration matrix

This matrix records how the LiveKit Agents documentation structure maps to `rtp-agent` at `v0.0.67`.

| LiveKit section | rtp-agent target | Status | Evidence | Action |
|---|---|---|---|---|
| Introduction | `/` | partial | `app/app.go`, `core/agent`, `interface/worker` | rewrite |
| Get Started / Introduction | `build-agents/get-started/introduction` | implemented | `cmd/main.go`, `app/app.go` | create |
| Voice AI quickstart | `build-agents/get-started/voice-ai-quickstart` | implemented | `examples/voice_agents/basic_agent` | create |
| Agent Builder | `build-agents/get-started/agent-builder` | planned/not implemented | no builder package | create status page |
| Agent Console | `build-agents/get-started/agent-console` | partial | `interface/cli/console` | create |
| Agent Embed Widget | `build-agents/get-started/agent-embed-widget` | planned/not implemented | no widget package | create status page |
| Prompting guide | `build-agents/get-started/prompting-guide` | partial | `core/agent/agent.go`, `core/llm/llm.go` | create |
| Multimodality | `build-agents/multimodality/overview` | partial | `core/llm`, `core/stt`, `core/tts`, `core/vad`, `core/agent/avatar.go` | create |
| Speech and audio | `build-agents/speech-audio/*` | partial | `core/agent/transcription.go`, `interface/worker/room_io.go` | create |
| Images and video | `build-agents/images-video/overview` | partial | `core/agent/video_sampler.go` | create |
| Logic and structure | `build-agents/logic-structure/*` | partial | `core/agent`, `core/beta/workflows` | create |
| Tools / pipeline nodes | `build-agents/tools/pipeline-nodes-hooks` | intentionally different | `core/agent/generation.go` | create |
| Turn detection and interruptions | `build-agents/turn-detection-interruptions/*` | partial | `core/agent`, `adapter/livekit`, `adapter/pipecat` | create |
| Testing and evaluation | `build-agents/testing-evaluation/*` | implemented | `core/evals`, `scripts/parity-*` | create |
| Prebuilt components/tasks/tools | `build-agents/prebuilt/overview` | partial | `core/beta`, `adapter/*` | create |
| Agent Server | `build-agents/agent-server/*` | implemented/partial | `interface/worker`, `interface/cli` | create |
| Models | `build-agents/models/*` | implemented/partial | `core/llm`, `core/stt`, `core/tts`, adapters | create |
| Provider sections | `build-agents/providers/*` | implemented by file | `adapter/*/{llm,stt,tts,realtime,avatar,vad}.go` | create |
| Agent Frontends | `agent-frontends/overview` | planned/not implemented | no frontend package | create status page |
| Telephony | `telephony/overview` | partial | `core/beta/tools`, `core/beta/workflows/warm_transfer.go` | create |
| WebRTC Transport | `webrtc-transport/overview` | partial | `interface/worker/room_io.go` | create |
| Manage and Deploy | `manage-deploy/*` | partial | `cmd/main.go`, `interface/worker/server.go` | rewrite/create |
| Reference | `reference/*` | implemented/partial | source packages and tests | create/rewrite |

