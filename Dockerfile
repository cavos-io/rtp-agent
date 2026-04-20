# ─────────────────────────────────────────────
# Stage 1: Builder
# ─────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

# Install CGO dependencies:
#   libopus-dev  → hraban/opus (Opus audio codec)
#   pkg-config   → CGO pkg-config detection
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus-dev \
    libopusfile-dev \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*

# Allow Go to auto-download the required toolchain version (go.mod requires 1.25)
ENV GOTOOLCHAIN=auto

WORKDIR /app

# Copy dependency manifests first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO enabled
RUN CGO_ENABLED=1 GOOS=linux go build -o agent ./cmd/main.go

# ─────────────────────────────────────────────
# Stage 2: Runtime
# ─────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# Install only runtime C libraries (libopus, not dev headers)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 \
    libopusfile0 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy compiled binary from builder
COPY --from=builder /app/agent ./agent

# Copy .env if present (optional — prefer env vars at runtime)
# COPY .env .env

# LiveKit credentials (override with docker run -e or docker-compose)
ENV AGENT_NAME=cavos-voice-agent
ENV LIVEKIT_URL=
ENV LIVEKIT_API_KEY=
ENV LIVEKIT_API_SECRET=
ENV OPENAI_API_KEY=
ENV ELEVENLABS_API_KEY=
ENV PPROF_ADDR=:6060

# Expose pprof port (optional, used for profiling)
EXPOSE 6060

# Default command: start worker mode (auto-dispatch)
ENTRYPOINT ["./agent"]
CMD ["start"]
