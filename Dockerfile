# ─────────────────────────────────────────────
# Stage 1: Builder
# ─────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

# Install CGO dependencies:
#   libopus-dev  → hraban/opus (Opus audio codec)
#   pkg-config   → CGO pkg-config detection
#   nasm         → FFmpeg assembly optimisations
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus-dev \
    libopusfile-dev \
    portaudio19-dev \
    pkg-config \
    nasm \
    wget \
    git \
    autoconf \
    automake \
    libtool \
    make \
    && rm -rf /var/lib/apt/lists/*

# Build FFmpeg n8.0 from source (required by go-astiav).
# Minimal build: only AAC encoder + MP4 muxer + swresample (no programs, no doc).
RUN git clone --depth 1 --branch n8.0 https://github.com/FFmpeg/FFmpeg /tmp/ffmpeg \
    && cd /tmp/ffmpeg \
    && ./configure \
        --prefix=/opt/ffmpeg \
        --enable-shared \
        --disable-static \
        --disable-programs \
        --disable-doc \
        --disable-everything \
        --enable-encoder=aac \
        --enable-muxer=mp4 \
        --enable-muxer=ipod \
        --enable-protocol=file \
        --enable-swresample \
    && make -j$(nproc) \
    && make install \
    && rm -rf /tmp/ffmpeg

ENV PKG_CONFIG_PATH="/opt/ffmpeg/lib/pkgconfig"
ENV CGO_CFLAGS="-I/opt/ffmpeg/include"
ENV CGO_LDFLAGS="-L/opt/ffmpeg/lib"

# Build RNNoise from source (not available in bookworm apt)
RUN git clone https://github.com/xiph/rnnoise.git /tmp/rnnoise \
    && cd /tmp/rnnoise \
    && ./autogen.sh \
    && ./configure --prefix=/usr/local \
    && make -j$(nproc) \
    && make install \
    && rm -rf /tmp/rnnoise

# Install ONNX Runtime v1.18.1 (required for Silero VAD)
ARG ONNXRUNTIME_VERSION=1.18.1
RUN wget -q https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz \
    && tar -xzf onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz \
    && cp onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/lib/* /usr/local/lib/ \
    && cp -r onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/include/* /usr/local/include/ \
    && ldconfig \
    && rm -rf onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}*

# Allow Go to auto-download the required toolchain version (go.mod requires 1.25)
ENV GOTOOLCHAIN=auto

WORKDIR /app

# Copy dependency manifests first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO enabled (ONNX Runtime linked via CGO)
ENV CGO_ENABLED=1
ENV C_INCLUDE_PATH=/usr/local/include
ENV LIBRARY_PATH=/usr/local/lib
ENV LD_RUN_PATH=/usr/local/lib
RUN CGO_ENABLED=1 GOOS=linux go build -o agent ./cmd/main.go

# ─────────────────────────────────────────────
# Stage 2: Runtime
# ─────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# Install only runtime C libraries (libopus, not dev headers)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 \
    libopusfile0 \
    libportaudio2 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy ONNX Runtime and RNNoise shared libraries from builder
COPY --from=builder /usr/local/lib/libonnxruntime* /usr/local/lib/
COPY --from=builder /usr/local/lib/librnnoise* /usr/local/lib/
# Copy FFmpeg shared libraries from builder (go-astiav runtime deps)
COPY --from=builder /opt/ffmpeg/lib/libavcodec.so* /usr/local/lib/
COPY --from=builder /opt/ffmpeg/lib/libavformat.so* /usr/local/lib/
COPY --from=builder /opt/ffmpeg/lib/libavutil.so* /usr/local/lib/
COPY --from=builder /opt/ffmpeg/lib/libswresample.so* /usr/local/lib/
RUN ldconfig

WORKDIR /app

# Copy compiled binary from builder
COPY --from=builder /app/agent ./agent

# LiveKit credentials (override with docker run -e or docker-compose)
ENV AGENT_NAME=cavos-voice-agent
ENV LIVEKIT_URL=
ENV LIVEKIT_API_KEY=
ENV LIVEKIT_API_SECRET=
ENV OPENAI_API_KEY=
ENV ELEVENLABS_API_KEY=
ENV PPROF_ADDR=:6060
ENV SILERO_VAD_MODEL_PATH=/models/silero_vad.onnx

# Expose pprof port (optional, used for profiling)
EXPOSE 6060

# Default command: start worker mode (auto-dispatch)
ENTRYPOINT ["./agent"]
CMD ["start"]
