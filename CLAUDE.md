# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**rtp-agent** is a Go-based real-time voice agent runtime built with a hexagonal architecture. It's designed as a practical alternative to LiveKit Agent SDK, prioritizing low end-to-end latency and reduced hardware requirements for voice and multimodal AI workflows.

### Core Goals
- Reduce latency in STT → LLM → TTS turn loops
- Minimize CPU and memory requirements for agent execution
- Maintain modularity through ports and adapters pattern for easy provider swaps

### Tech Stack
- **Language**: Go 1.25+
- **WebRTC**: Pion WebRTC v4
- **Realtime Communication**: LiveKit Server SDK for Go v2
- **Audio Codecs**: Opus (hraban/opus), Ogg Vorbis (jfreymuth/oggvorbis), MP3 (hajimehoshi/go-mp3)
- **Provider Adapters**: 60+ adapters in `adapter/` covering LLM, STT, TTS, VAD, and avatar services (OpenAI, Anthropic, Google, AWS, ElevenLabs, Deepgram, etc.)
- **Observability**: OpenTelemetry for traces and metrics

## Architecture

The project follows **hexagonal architecture** (ports and adapters pattern) with five key layers:

```
adapter/          # External system implementations (LLM, STT, TTS, avatar services)
core/             # Business logic (pure domain logic, no external deps)
  ├─ agent/       # Agent lifecycle, session, activity, pipeline
  ├─ llm/         # LLM abstraction, chat context, tools
  ├─ stt/         # Speech-to-text abstraction
  ├─ tts/         # Text-to-speech abstraction
  ├─ vad/         # Voice activity detection abstraction
  ├─ audio/       # Audio processing utilities
  ├─ inference/   # Inference utilities
  ├─ beta/        # Experimental features
  └─ evals/       # Evaluation framework
interface/        # Input/output adapters
  ├─ worker/      # LiveKit worker transport, RoomIO, job handling
  └─ cli/         # CLI commands (start, dev, connect)
library/          # Shared utilities
  ├─ logger/      # Structured logging (slog wrapper)
  ├─ telemetry/   # OpenTelemetry integration
  ├─ tokenize/    # Text tokenization
  ├─ math/        # Math utilities
  └─ utils/       # General utilities
model/            # Shared data models (AudioFrame, etc.)
cmd/              # Application entry point
  ├─ main.go      # Bootstrap, credential setup, agent pipeline
  └─ dispatch/    # Job dispatch logic
```

### Key Architecture Patterns

**Ports and Adapters**: Core defines interfaces (ports) for external operations:
- `core/llm.LLM` → `adapter/openai`, `adapter/anthropic`, etc.
- `core/stt.STT` → `adapter/deepgram`, `adapter/google`, etc.
- `core/tts.TTS` → `adapter/elevenlabs`, `adapter/google`, etc.
- `core/vad.VAD` → Simple implementations or external services

**Agent Pipeline Flow**:
1. **RoomIO** (interface/worker) connects to LiveKit room and manages audio I/O
2. **VAD Stream** detects speech in incoming audio frames
3. **STT Stream** transcribes detected speech
4. **LLM Inference** generates assistant response
5. **TTS Stream** synthesizes response audio
6. **Audio Publication** sends audio back to room via WebRTC

**Chat Context Management**:
- `core/llm/ChatContext` maintains conversation history
- Supports message truncation, merging, and provider format conversion
- Preserves system/developer instructions when truncating

**Job and Session Lifecycle**:
- `AgentServer` receives LiveKit job assignments
- `JobContext` wraps the room connection and provides access to participants
- `AgentSession` orchestrates STT, VAD, LLM, TTS interaction
- `PipelineAgent` implements the core streaming pipeline

## Development Environment

### Prerequisites
- Go 1.25+ (see go.mod)
- For audio codec support: libopus, libopusfile (dev headers)
- LiveKit server URL with credentials (see .env.example)

### Build and Run

**Build the agent**:
```bash
go build -o agent ./cmd/main.go
```

**Build with Docker** (recommended for deployment):
```bash
docker build -t cavos-rtp-agent:latest .
docker compose up -d
```

**Run the agent** (requires LiveKit credentials in environment):
```bash
go run ./cmd/main.go start      # Worker mode: auto-dispatch from LiveKit
go run ./cmd/main.go dev        # Development mode: auto-reload on file changes
go run ./cmd/main.go connect <room_name> [participant_identity]  # Connect mode: join specific room
```

### Configuration

Create a `.env` file in the root (see `.env.example` for template):
```
LIVEKIT_URL=wss://your-livekit-server
LIVEKIT_API_KEY=your-api-key
LIVEKIT_API_SECRET=your-api-secret
OPENAI_API_KEY=sk-proj-...
ELEVENLABS_API_KEY=sk_...
PPROF_ADDR=:6060
```

### Testing

**Run all tests**:
```bash
go test ./...
```

**Run tests with coverage**:
```bash
go test -cover ./...
```

**Run a single test package**:
```bash
go test ./core/agent -v
```

**Run a specific test**:
```bash
go test ./core/agent -run TestAgentSession -v
```

Note: There is currently minimal test coverage (one test file: `interface/worker/pre_connect_audio_test.go`). Most testing is manual or integration-based against a LiveKit server.

### Linting and Formatting

The project uses `gopls` with strict static analysis enabled (see `.vscode/settings.json`):
```bash
go fmt ./...    # Format Go files
go vet ./...    # Run staticcheck and other linters
```

On save in VS Code, Go files are auto-formatted and imports auto-organized.

### Dependency Management

```bash
go mod download
go mod tidy
```

For private modules, use the `goget.sh` script:
```bash
export GITHUB_ACCESS_TOKEN=<your-token>
./goget.sh
```

## Key Files and Responsibilities

### Entry Point: `cmd/main.go`
- Loads `.env` credentials
- Starts pprof HTTP server (port 6060 by default) for memory profiling
- Creates `AgentServer` with LiveKit connection details
- Registers the RTC session callback that:
  - Initializes provider adapters (LLM, STT, TTS, VAD)
  - Creates `ChatContext` with system prompt
  - Connects to LiveKit room via `JobContext`
  - Creates `AgentSession` and `RoomIO`
  - Manages the full session lifecycle and cleanup

### Agent Core: `core/agent/`
- **`agent.go`**: Base `Agent` struct with instructions, tools, provider config
- **`agent_session.go`**: Orchestrates entire agent conversation session, manages user/agent state, handles turn-taking
- **`pipeline_agent.go`**: Implements the streaming pipeline (VAD → STT → LLM → TTS)
- **`generation.go`**: Utilities for LLM inference and TTS inference
- **`agent_activity.go`**: Tracks agent activity and state transitions
- **`events.go`**: Domain events emitted during session

### LLM Core: `core/llm/`
- **`llm.go`**: `LLM` interface (Chat, Completion) and data types (ChatMessage, FunctionCall, etc.)
- **`chat_context.go`**: Conversation history management, truncation, merging, provider format conversion

### Audio Handling: `core/audio/`, `core/stt/`, `core/tts/`, `core/vad/`
- **`stt.go`**: `STT` interface with streaming and batch recognition
- **`tts.go`**: `TTS` interface with streaming and chunked synthesis
- **`vad.go`**: `VAD` interface for voice activity detection
- Audio frames are normalized to `model.AudioFrame` (bytes, sample rate, channels)

### LiveKit Worker: `interface/worker/`
- **`server.go`**: `AgentServer` manages WebSocket connection to LiveKit, job dispatching
- **`room_io.go`**: `RoomIO` wraps the LiveKit room, manages audio I/O (decode/encode Opus), recording
- **`job.go`**: `JobContext` represents a single agent job with room connection

### Provider Adapters: `adapter/<provider>/`
Each adapter package implements one or more of the core interfaces:
- `adapter/openai/llm.go` → `core/llm.LLM` (Chat API)
- `adapter/openai/stt.go` → `core/stt.STT` (Whisper API)
- `adapter/elevenlabs/tts.go` → `core/tts.TTS` (Streaming WebSocket)

Adding a new provider:
1. Create `adapter/<provider>/` package
2. Implement the needed interface(s) from `core/` (LLM, STT, TTS, VAD, Avatar, etc.)
3. Initialize in `cmd/main.go` and pass to agent

### Streaming Patterns

All streaming operations use channel-based pipelines to avoid blocking:
- Frames pushed into stream via `PushFrame()`
- Events returned via `Next()` (blocking, returns when data available)
- Streams can be flushed with `Flush()` and closed with `Close()`
- Provider implementations may spin up goroutines to handle async operations

## Important Implementation Details

### Interrupt Handling
- Turn detection supports VAD-based interruption (agent stops and listens when user speaks)
- `MinEndpointingDelay` and `MaxEndpointingDelay` control how long agent waits after user stops speaking

### Memory Management
- Session cleanup is explicit: contexts are cancelled, references nulled, GC forced
- Pion WebRTC resources (TURN, ICE, DTLS, SCTP) require async cleanup time (3s sleep built-in)

### Audio Encoding
- Input audio is Opus-encoded from LiveKit peers, decoded to PCM for processing
- Output audio is synthesized as PCM from TTS, encoded to Opus for publication
- Frame metadata includes sample rate and channel count (usually 16kHz, mono or stereo)

### Chat Context Truncation
- System/developer messages are preserved when context is truncated to fit token limits
- Function calls are excluded from truncation start to avoid partial sequences

### VAD and STT Integration
- `core/stt.StreamAdapter` wraps a non-streaming STT (like OpenAI Whisper) and handles buffering
- `core/vad.SimpleVAD` provides basic voice detection for turn-taking

## Observability

- **Logging**: Uses `log/slog` via `library/logger.Logger`
- **Tracing**: OpenTelemetry integration in `library/telemetry/`
- **Metrics**: Prometheus-compatible metrics (token usage, latency, etc.)
- **Profiling**: pprof HTTP server on `PPROF_ADDR` (default :6060) for CPU/memory profiling

## Deployment

### Docker
The `Dockerfile` is a two-stage build:
1. Builder stage: compiles Go binary with CGO for audio codec support
2. Runtime stage: minimal Debian image with only libopus/libopusfile runtime deps

### Docker Compose
`docker-compose.yml` provides local development setup with environment variable override support.

### Environment Variables
All credentials and settings are environment-based (no config files required):
- `LIVEKIT_URL`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`
- `OPENAI_API_KEY`, `ELEVENLABS_API_KEY`, `GOOGLE_API_KEY`, etc.
- `PPROF_ADDR` for profiling endpoint

## Code Conventions

All code changes **must follow existing structure and patterns** in the codebase:

- **Hexagonal architecture**: new external integrations go in `adapter/`, business logic in `core/`, I/O adapters in `interface/`
- **Interface compliance**: new providers must implement the corresponding `core/` interface (`LLM`, `STT`, `TTS`, `VAD`, etc.)
- **Streaming pattern**: use `PushFrame()` / `Next()` / `Flush()` / `Close()` — channel-based, non-blocking
- **Error handling**: follow the same patterns as neighboring code in the package
- **Logging**: use `library/logger.Logger` (slog wrapper), not `fmt.Println` or `log.*`
- **Context propagation**: pass `context.Context` through all async operations for clean shutdown
- **Naming**: match Go conventions and existing naming style in the package

Before committing, run `/cr` to review changes against these conventions.

## Commit Workflow

This repo uses **micro commits** with **conventional commit** format. Use `/mc` to commit changes.

- Run `/cr` first to review code quality and pattern compliance
- Each commit should be one atomic, logical change
- Format: `type(scope): description` (e.g., `feat(adapter/azure): add Azure STT provider`)
- Types: `feat`, `fix`, `refactor`, `perf`, `docs`, `test`, `chore`, `style`
- Scope = affected package or area (omit if change spans many areas)
- If a change impacts architecture, flow, build process, or behavior — update this CLAUDE.md file in the same commit

## CI/CD

**GitHub Actions** workflow (`deploy-docs.yml`):
- Builds Docusaurus site (`docs/website/`) and deploys to GitHub Pages
- No automated Go tests or builds in CI currently

## Other Notable Files

- **`ARCHITECTURE.md`**: Detailed hexagonal architecture explanation
- **`gap_analysis.md`**: Feature gap analysis vs. LiveKit Agent SDK
- **`scripts/loadtest.sh`**: Load testing for concurrent agent sessions
- **`resources/`**: Background ambience audio assets (OGG files)
- **`recordings/`**: Session recordings saved during agent runs
