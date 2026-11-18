# Build from the repository root. No credentials or STG access required.
FROM golang:1.26-trixie AS native-runtime-check
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY scripts/install-livekit-ffi-linux.sh /tmp/install-ffi.sh
RUN bash /tmp/install-ffi.sh "$TARGETARCH" /opt/livekit
COPY go.mod go.sum ./
COPY interface/worker/livekit/nativeinprocess ./interface/worker/livekit/nativeinprocess
COPY interface/worker/livekit/ffipb ./interface/worker/livekit/ffipb
ENV LIVEKIT_LIB_PATH=/opt/livekit/liblivekit_ffi.so
RUN CGO_ENABLED=1 go test -tags nativeffi ./interface/worker/livekit/nativeinprocess -run '^TestPinnedNativeLibrarySmoke$' -count=1
RUN CGO_ENABLED=1 go test -race ./interface/worker/livekit/nativeinprocess -count=1
