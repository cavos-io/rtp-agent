# Agora RTC Worker Transport

`rtp-agent` supports an optional Agora RTC worker transport behind the
`agora_sdk` build tag. Default builds keep using the stubbed constructor so CI
does not require Agora native headers or shared libraries.

## Build Prerequisites

The Agora Golang Server SDK Go module does not include the native `agora_sdk/`
directory in the module zip. To build with `-tags agora_sdk`, install or check
out the SDK repository with native dependencies:

```sh
git clone https://github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK.git
cd Agora-Golang-Server-SDK
git checkout release/2.6.1
make deps
make install
```

Use a local `replace` while building tagged binaries if the native SDK checkout
is not available through the module cache:

```go
replace github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2 => /path/to/Agora-Golang-Server-SDK
```

The SDK expects its native files at `agora_sdk/` relative to the SDK checkout.
At runtime, include that directory in the dynamic linker path:

```sh
export LD_LIBRARY_PATH=/path/to/Agora-Golang-Server-SDK/agora_sdk:$LD_LIBRARY_PATH
```

## Running

Build with the tag and configure Agora credentials:

```sh
go build -tags agora_sdk ./cmd

export RTP_AGENT_TRANSPORT=agora
export AGORA_APP_ID=...
export AGORA_APP_CERTIFICATE=...
export AGORA_CHANNEL=...
export AGORA_UID=agent-0
```

If `AGORA_TOKEN` is set, the worker uses it as-is. If `AGORA_TOKEN` is empty
and `AGORA_APP_CERTIFICATE` is set, the worker generates a one-hour publisher
RTC token for the configured channel and UID. `AGORA_TOKEN` is optional only
when authentication is disabled for the Agora project or `AGORA_APP_CERTIFICATE`
is available for token generation. If `AGORA_UID` is omitted, the SDK transport
uses `"0"`, matching the Agora server SDK examples.
