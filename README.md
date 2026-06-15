# RTP Agent

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Docs](https://img.shields.io/badge/Docs-Docusaurus-2ea44f)](https://cavos-io.github.io/rtp-agent/)
[![Architecture](https://img.shields.io/badge/Architecture-Hexagonal-0A66C2)](./ARCHITECTURE.md)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Status](https://img.shields.io/badge/Status-Experimental-orange)](https://github.com/cavos-io/rtp-agent)

Golang-first effort to build a practical alternative to the LiveKit Agent SDK, focused on lower end-to-end latency and reduced hardware requirements.

Documentation: https://cavos-io.github.io/rtp-agent/

## Why This Project

`rtp-agent` explores a lean, production-friendly agent runtime in Go for real-time voice and multimodal workflows.

Core goals:
- Reduce latency across STT -> LLM -> TTS turn loops.
- Minimize CPU and memory requirements so agents can run on smaller instances.
- Keep architecture modular through ports and adapters, so providers can be swapped quickly.

## Table of Contents

- [Features](#features)
- [Built With](#built-with)
- [Getting Started](#getting-started)
- [Usage](#usage)
- [Roadmap](#roadmap)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [License](#license)
- [Acknowledgments](#acknowledgments)

## Features

- Go-based worker runtime and CLI entrypoint.
- Hexagonal architecture (`adapter`, `core`, `interface`, `library`) for clear separation of concerns.
- Broad provider coverage with 60+ adapter packages (LLM, STT, TTS, avatar, utilities).
- LiveKit-compatible worker transport and job lifecycle handling.
- Optional Agora RTC worker transport for joining Agora channels directly as an agent.
- Docusaurus documentation site for adapter references.

## Built With

- [Go](https://go.dev/)
- [LiveKit Server SDK for Go](https://github.com/livekit/server-sdk-go)
- [Pion WebRTC](https://github.com/pion/webrtc)
- [Docusaurus](https://docusaurus.io/) (documentation site)

## Getting Started

### Prerequisites

- Go 1.25+ (as defined in `go.mod`)
- Access to a LiveKit deployment (URL, API key, API secret)
- For Agora RTC transport: an `Agora-Golang-Server-SDK` checkout with native SDK assets and Agora project credentials.

### Installation

```bash
git clone https://github.com/cavos-io/rtp-agent.git
cd rtp-agent
go mod tidy
```

### Configure Worker Options

Current bootstrap is code-first. Update worker options in [`cmd/main.go`](./cmd/main.go) before running:

```go
opts := worker.WorkerOptions{
  AgentName:  "example-agent",
  WorkerType: worker.WorkerTypeRoom,
  WSRL:       "wss://<your-livekit-host>",
  APIKey:     "<your-api-key>",
  APISecret:  "<your-api-secret>",
}
```

## Usage

Run the worker:

```bash
go run ./cmd/main.go start
```

Development mode with autoreload:

```bash
go run ./cmd/main.go dev
```

Connect mode for local room testing:

```bash
go run ./cmd/main.go connect <room_name> [participant_identity]
```

### Agora RTC Worker Transport

The Agora transport is optional and built behind the `agora_sdk` tag because it depends on Agora native runtime libraries.

Build an Agora-enabled binary:

```bash
AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK \
  OUT=.tmp/rtp-agent-agora \
  scripts/build-agora-sdk.sh
```

Run the worker in an Agora RTC channel:

```bash
export RTP_AGENT_TRANSPORT=agora
export AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK
export LD_LIBRARY_PATH="$AGORA_GO_SDK_DIR/agora_sdk:$LD_LIBRARY_PATH"
export AGORA_APP_ID=<agora-app-id>
export AGORA_APP_CERTIFICATE=<agora-app-certificate> # or set AGORA_TOKEN
export AGORA_CHANNEL=<channel-name>
export AGORA_UID=agent-0

.tmp/rtp-agent-agora start
```

Validate the join path with the smoke script:

```bash
AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK \
  AGORA_APP_ID=<agora-app-id> \
  AGORA_APP_CERTIFICATE=<agora-app-certificate> \
  AGORA_CHANNEL=<channel-name> \
  AGORA_UID=agent-0 \
  scripts/smoke-agora-rtc.sh
```

The smoke script succeeds only after the worker logs `agora transport connected` for a stable window and prints:

```text
Agora RTC connected
```

## Roadmap

- [ ] End-to-end latency profiling and optimization pass.
- [ ] Lower-memory execution mode for constrained environments.
- [ ] Compatibility layer and integrations for [Pipecat](https://github.com/pipecat-ai/pipecat).
- [ ] Compatibility layer and integrations for [VisionAgent](https://github.com/GetStream/vision-agents).
- [ ] Additional open-source integration target: [Vocode](https://github.com/vocodedev/vocode-core).

## Documentation

- Public docs site: [https://cavos-io.github.io/rtp-agent/](https://cavos-io.github.io/rtp-agent/)
- Architecture overview: [`ARCHITECTURE.md`](./ARCHITECTURE.md)

## Contributing

Contributions are welcome. Open an issue for bug reports or feature proposals, and submit a PR for improvements.

Repository: https://github.com/cavos-io/rtp-agent

## License

This project is licensed under the MIT License.

See [`LICENSE`](./LICENSE) for details.

## Acknowledgments

- [LiveKit](https://github.com/livekit) for the reference agent model and ecosystem.
- [Pion](https://github.com/pion) for core WebRTC building blocks in Go.
- The broader open-source realtime AI community.
