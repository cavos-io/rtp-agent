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

Before a tagged build, run the preflight check:

```sh
AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK scripts/check-agora-sdk.sh
```

Build the `rtp-agent` command with the SDK tag and a temporary local module
replacement:

```sh
AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK \
  OUT=.tmp/rtp-agent-agora \
  scripts/build-agora-sdk.sh
```

## Running

Configure Agora credentials and run the tagged binary:

```sh
export RTP_AGENT_TRANSPORT=agora
export AGORA_APP_ID=...
export AGORA_APP_CERTIFICATE=...
export AGORA_CHANNEL=...
export AGORA_UID=agent-0
export AGORA_JOIN_TIMEOUT=10s
export AGORA_SDK_DATA_DIR=.tmp/agora-sdk-runtime
export LD_LIBRARY_PATH=/path/to/Agora-Golang-Server-SDK/agora_sdk:$LD_LIBRARY_PATH

.tmp/rtp-agent-agora start
```

If `AGORA_TOKEN` is set, the worker uses it as-is. If `AGORA_TOKEN` is empty
and `AGORA_APP_CERTIFICATE` is set, the worker generates a one-hour publisher
RTC token for the configured channel and UID. `AGORA_TOKEN` is optional only
when authentication is disabled for the Agora project or `AGORA_APP_CERTIFICATE`
is available for token generation. If `AGORA_UID` is omitted, the SDK transport
uses `"0"`, matching the Agora server SDK examples.

`AGORA_JOIN_TIMEOUT` bounds how long the worker waits for Agora's connected
event before startup fails. It accepts Go duration strings such as `10s` or a
number of seconds.

`AGORA_SDK_DATA_DIR` controls where the native Agora SDK writes logs, config,
and cache files. Set it to a writable runtime directory instead of letting SDK
artifacts appear in the process working directory.

## Smoke Test

With valid Agora credentials, run:

```sh
AGORA_GO_SDK_DIR=/path/to/Agora-Golang-Server-SDK \
  AGORA_APP_ID=... \
  AGORA_APP_CERTIFICATE=... \
  AGORA_CHANNEL=... \
  AGORA_UID=agent-0 \
  AGORA_SMOKE_TIMEOUT=30 \
  AGORA_SMOKE_STABLE_SECONDS=2 \
  scripts/smoke-agora-rtc.sh
```

The smoke test builds the tagged binary, starts the worker with
`RTP_AGENT_TRANSPORT=agora`, waits for the `agora transport connected` log, and
then requires the worker to stay healthy for `AGORA_SMOKE_STABLE_SECONDS`
seconds before printing `Agora RTC connected`. It fails fast if the SDK emits an
Agora transport error event or the worker logs an error. The smoke helper also
defaults Go build caches and SDK runtime output under `.tmp/` so the check can
run in a restricted workspace.

`AGORA_SMOKE_TIMEOUT` and `AGORA_SMOKE_STABLE_SECONDS` are non-negative integer
second counts. The default timeout is 30 seconds, and the default stable window
is 2 seconds.
