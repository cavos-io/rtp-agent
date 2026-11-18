ARG RUNTIME_IMAGE
# Stage 1: Build the Go binary
FROM golang:1.26-trixie AS build
WORKDIR /app

ARG GL_USERNAME
# Build support only; runtime selection remains opt-in.
ARG ENABLE_NATIVE=false
ARG TARGETARCH
ENV GOPRIVATE="github.com/cavos-io/*"

RUN apt update && \
    apt install -y --no-install-recommends \
      ca-certificates \
      curl \
      unzip \
      git \
      openssh-client \
      libavcodec-dev \
      libavformat-dev \
      libavutil-dev \
      libswresample-dev \
      libopus-dev \
      libopusfile-dev \
      portaudio19-dev \
      pkg-config && \
    rm -rf /var/lib/apt/lists/*

COPY . .
# Validation-only: preserve the checkout replacement without editing go.mod.
COPY --from=rtp-source / /rtp-agent

RUN --mount=type=secret,id=gl_token,required=false \
    --mount=type=ssh,required=false \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    if [ -s /run/secrets/gl_token ]; then \
      token="$(cat /run/secrets/gl_token)" && \
      git config --global url."https://${GL_USERNAME:-x-access-token}:${token}@github.com/".insteadOf "https://github.com/"; \
    elif [ -S "${SSH_AUTH_SOCK:-}" ]; then \
      mkdir -p /root/.ssh && ssh-keyscan -H github.com > /root/.ssh/known_hosts && \
      git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"; \
    else \
      echo "GitHub credentials are required for private modules" >&2; exit 1; \
    fi && \
    go mod tidy && \
    rm -f /root/.gitconfig


RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /validation && \
    CGO_ENABLED=1 go test -tags ffmpeg,nativeinprocess,nativeacceptance ./app -run '^$' -c -o /validation/downstream.test && \
    cd /rtp-agent && CGO_ENABLED=1 go test -tags ffmpeg,nativeinprocess,nativeroomintegration ./examples/starter -run '^$' -c -o /validation/starter.test
FROM ${RUNTIME_IMAGE}
COPY --from=build --chmod=755 /validation /validation
ENTRYPOINT ["/validation/downstream.test"]
