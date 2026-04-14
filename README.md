# RTP Agent

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
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

## Features

- **Pluggable VAD Architecture**: Natively integrated support for Silero (Default) and TEN-VAD.
- **Go-based Worker**: High-performance runtime and CLI entrypoint.
- **Hexagonal Architecture**: Modular separation of concerns (`adapter`, `core`, `interface`, `library`).
- **Broad Provider Coverage**: 60+ adapter packages for various AI services.
- **Auto Environment Loading**: Native `.env` file support.

## Getting Started

### Prerequisites

- Go 1.22+
- LiveKit Server deployment (URL, API Key, Secret)
- [CGO] ONNX Runtime library (`onnxruntime.dll` for Windows, `.so` for Linux)
- [VAD] Silero model file (`silero_vad.onnx`) in the root directory.

### Environment Setup

Create a `.env` file in the root directory:

```env
LIVEKIT_URL=wss://your-livekit-host
LIVEKIT_API_KEY=your-api-key
LIVEKIT_API_SECRET=your-api-secret
OPENAI_API_KEY=your-openai-key
ELEVENLABS_API_KEY=your-eleven-key
VAD_TYPE=silero # Options: silero, tenvad
```

#### Windows Development (CGO Setup)
If you are developing on Windows, run these commands in your PowerShell session to satisfy CGO dependencies:
```powershell
$env:CGO_CFLAGS="-I$PWD/include -I$PWD/include/opus"
$env:CGO_LDFLAGS="-L$PWD -L$PWD/bin"
$env:CGO_ENABLED="1"
```

## Usage

You can now run the agent directly using standard Go commands.

### Run the Worker
```bash
go run ./cmd/main.go start
```

### Development Mode (with Autoreload)
```bash
go run ./cmd/main.go dev
```

### Connect to a Specific Room
```bash
go run ./cmd/main.go connect <room_name> [participant_identity]
```

## Roadmap

- [ ] End-to-end latency profiling and optimization pass.
- [ ] Lower-memory execution mode for constrained environments.
- [ ] Compatibility layer and integrations for [Pipecat](https://github.com/pipecat-ai/pipecat).

## License

This project is licensed under the MIT License. See [`LICENSE`](./LICENSE) for details.

## Acknowledgments

- [LiveKit](https://github.com/livekit) for the reference agent model.
- [Pion](https://github.com/pion) for core WebRTC building blocks in Go.
