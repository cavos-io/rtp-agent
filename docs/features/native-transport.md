# Using the native LiveKit transport

The native transport runs LiveKit FFI in the worker process for room media.
It is opt-in: **Pion remains the default**, including in binaries built with
`nativeinprocess`. The supported FFI version is **0.12.80**.

This guide covers the shared worker lifecycle. It does not require the local
experimental probes or a custom native startup function in your application.

## Prerequisites

- The Go toolchain specified in `go.mod`, CGO enabled, and a C compiler.
- Linux or macOS on amd64 or arm64. The downloader selects the platform of the
  process running it; it has no cross-platform download flag.
- A configured LiveKit worker with `LIVEKIT_URL`, `LIVEKIT_API_KEY`, and
  `LIVEKIT_API_SECRET`, plus the credentials and models required by its providers.
- A compatible operating-system runtime for the downloaded shared library.
  Shipping the `.so` alone does not install its system dependencies.

## Download, build, and start

Run these commands from the repository root using an already configured starter
application. Its entrypoint initializes providers before dispatching CLI commands,
so provider configuration must also be valid for `download-files`.

First remove legacy probe selectors from your shell and any application-loaded
`.env` file:

```bash
unset RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS
unset RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM
unset RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE
```

Download the library before selecting native:

```bash
RTP_AGENT_LIVEKIT_TRANSPORT=pion go run ./examples/starter download-files
```

`download-files` invokes all registered plugin downloaders, including
`rtp-agent.transport.livekit-native`. It verifies the pinned native archive's
SHA-256 checksum and installs the library atomically. It does not select native
or require the `nativeinprocess` build tag.

The default destination is relative to the process working directory:

| Platform | Library path |
| --- | --- |
| Linux | `resources/lib/livekit/liblivekit_ffi.so` |
| macOS | `resources/lib/livekit/liblivekit_ffi.dylib` |

To use another location, set `LIVEKIT_LIB_PATH` to the **full library filename**
before downloading and starting. The override controls both installation and
loading; it does not override the pinned download version. An absolute path avoids
working-directory differences between build and deployment.

Build and run:

```bash
mkdir -p .tmp
CGO_ENABLED=1 go build -tags nativeinprocess -o .tmp/rtp-agent-native ./examples/starter
RTP_AGENT_TRANSPORT=livekit RTP_AGENT_LIVEKIT_TRANSPORT=native \
  .tmp/rtp-agent-native start
```

For an existing room, the same binary supports:

```bash
RTP_AGENT_TRANSPORT=livekit RTP_AGENT_LIVEKIT_TRANSPORT=native \
  .tmp/rtp-agent-native connect room-name participant-identity
```

Replace the starter package with your own worker entrypoint when deploying.
Package the library for the **deployment OS and architecture** alongside the
binary, and set the working directory or `LIVEKIT_LIB_PATH` accordingly.
Startup does not download missing libraries automatically.

## Integrate with an application

Applications using `app.DefaultConfigFromEnv()` receive the transport factory in
`config.WorkerOptions`. Pass those options to `worker.NewAgentServer` and keep the
ordinary session startup inside the job callback:

```go
session := agent.NewAgentSession(baseAgent, jobContext.Room, agent.AgentSessionOptions{})
if err := jobContext.StartSession(ctx, session); err != nil {
    return err
}
```

Do not connect `jobContext.Room` through Pion before starting a managed native
session. Do not add a per-job native runtime or call `CloseInProcessRuntime` from
individual jobs. The shared provider acquires the process runtime lazily and the
worker closes it after jobs settle. Runtime disposal is terminal for that process.

For applications constructing `worker.WorkerOptions` directly, environment
selection is not automatic. Set `SessionTransportFactory` explicitly:

```go
library, err := nativeroom.NativeLibraryPath()
if err != nil {
    return err
}
opts.SessionTransportFactory = func(w worker.WorkerOptions) (worker.SessionTransportProvider, error) {
    return nativeroom.NewProvider(nativeroom.Config{Library: library}, w.APIKey, w.APISecret)
}
```

Import `github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom` for this
API. Return a fresh provider per server. A nil factory retains Pion. Applications
with explicit `StartSessionOptions.Transport` or manual native runtime ownership
must handle that integration separately; managed and manual native ownership
cannot be mixed.

## Configuration and supported options

| Setting | Behavior |
| --- | --- |
| `RTP_AGENT_TRANSPORT=livekit` | Selects the worker backend; this is also the default. `native` is not a value for this variable. |
| `RTP_AGENT_LIVEKIT_TRANSPORT=native` | Opts into native when using `app.DefaultConfigFromEnv()`. Empty or `pion` selects Pion. |
| `LIVEKIT_LIB_PATH` | Overrides the native library filename. |
| `RTP_AGENT_NATIVE_PARTICIPANT` | Optionally restricts the remote participant selected by native. |

Set configuration before calling `app.DefaultConfigFromEnv()`; it captures the
selection for subsequent worker jobs.

The native room supports these `RoomOptions`:

- `ParticipantIdentity`, provided it does not conflict with native participant selection.
- `DisableTextInput` and `TextInputCallback`.
- `DisableTranscriptionOutput` and `TranscriptionNextOutput`.
- `RecordRemoteParticipants`.
- `DisablePreConnectAudio` and `PreConnectAudioTimeout` (nonnegative).

For example, disable frontend captions without disabling audio:

```go
err := jobContext.StartSession(ctx, session, worker.StartSessionOptions{
    RoomOptions: worker.RoomOptions{DisableTranscriptionOutput: true},
})
```

Pre-connect audio is enabled by default. It replays buffered startup audio from a
compatible client before delivering queued live input; the client must send that
buffered audio. Set `DisablePreConnectAudio: true` to opt out.

Native rejects unsupported options instead of silently ignoring them. These include
custom audio track names, input/output sample-rate overrides, input frame-size and
processor overrides, subscription timeout, disabling audio input/output,
`TranscriptionJSONFormat`, `DisableCloseOnDisconnect`, room deletion options, and
participant-kind filters. Pion-specific `ConnectOptions` and `RoomCallback` are
also unsupported. See [option validation](../../interface/worker/livekit/nativeroom/room_options.go)
for the exact contract. Keep applications needing those options on Pion.

## Verify selection and troubleshoot

On an active native session, look for `experimental native primary room enabled`
with `in_process=true`. The wording still says experimental. PCM continuity and
`experimental native in-process cleanup` logs provide delivery and teardown
information. Building with the tag or downloading the library alone does not prove
that a call used native.

| Symptom | Action |
| --- | --- |
| `native transport requires building with -tags nativeinprocess` | Rebuild the application with CGO and the native tag. |
| `native library (run download-files or set LIVEKIT_LIB_PATH)` | Download first; verify the working directory and library filename. |
| Shared-library load failure | Verify OS/architecture, runtime dependencies, and the pinned FFI version. |
| `conflicting native transport selection` | Remove the legacy `RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS=1` setting when selecting Pion. |
| Probe flag is only supported by local test harnesses | Unset `RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM` and `RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE`. |
| Unsupported room option | Remove the option if unnecessary, or select Pion. |
| `cannot mix managed and manual native runtime ownership` | Use one ownership model throughout the process. |

To roll back, drain and restart the worker with
`RTP_AGENT_LIVEKIT_TRANSPORT=pion`, removing legacy native selectors. The same
native-enabled binary can use Pion. This is a restart-time selection, not an
in-call transport switch. Explicitly supplied transport factories or per-session
transports must also be removed when reverting a code-configured application.

## Source reference

- [Environment selection](../../app/session_transport.go)
- [Managed runtime ownership](../../interface/worker/livekit/nativeroom/provider.go)
- [Library download and platform checksums](../../interface/worker/livekit/nativeroom/download.go)
- [FFI schema provenance](../../interface/worker/livekit/ffipb/SOURCE.md)

The native starter build command was verified for this documentation change.
A live download and provider-backed call were not rerun.
