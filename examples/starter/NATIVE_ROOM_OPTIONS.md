# In-process RoomOptions wiring

Added after `103faf62e`, 2026-11-17. Native remains opt-in. No helper or default-Pion behavior changes.

`JobContext.StartSession` now calls an optional `ConfigureRoomOptions(RoomOptions) error` method on custom transports before assigning media ownership or starting recording. This hook must only validate/configure, not acquire resources. Existing transports without the hook are unchanged. The example native Room implements it only for in-process mode; helper mode remains a no-op.

Supply the downstream options through the existing entry point:

```go
transport := nativeroom.NewInProcess(job, session, nativeConfig, apiKey, apiSecret)
err := job.StartSession(ctx, session, worker.StartSessionOptions{
    Transport: transport,
    RoomOptions: worker.RoomOptions{
        DisablePreConnectAudio:     true,
        DisableTranscriptionOutput: disableCaptions,
        TranscriptionNextOutput:    nextOutput,
        ParticipantIdentity:        callerIdentity,
    },
})
```

- `DisablePreConnectAudio` must be true. False requests unsupported behavior and fails before native startup. The native starter and ignored local example explicitly set true; their existing behavior is preserved. Direct callers must do the same.
- `ParticipantIdentity` feeds native discovery. If it differs from an explicitly configured native participant (including the environment-derived selector), startup fails rather than silently choosing one. Empty retains the existing selector/discovery behavior.
- `DisableTranscriptionOutput` is passed into the shared caption engine. True disables caller and agent transcription listeners, including the next-output callback, matching default RoomIO. Client-state and DTMF listeners remain active.
- `TranscriptionNextOutput` receives agent transcription text and final flushes through the existing RoomIO implementation. It is not a caller-transcription callback. Its existing error logging and execution semantics are reused.

This is not general RoomOptions support. `TextInputCallback` and `RecordRemoteParticipants` remain unsupported by the native adapter, along with other previously documented options. Do not treat this change as certification of the downstream application's complete configuration. Production placement of the experimental adapter also remains pending.

## Verification

Source contract: default RoomIO transcription listener gates and `forwardAgentTranscriptionNextOutput`. New tests exercise worker option delivery before startup, rejection without ownership/close, caller selector conflict, helper preservation, native caption-option transfer, caller/agent enabled and disabled captions, incremental/final next-output ordering and flush. Shared caption lifecycle tests cover close/interruption behavior.

Affected default suites (worker, LiveKit room, native-room adapter, starter) passed. Focused native-tagged race tests, architecture and vet results are retained under `examples/native-receive-probe/results/native-room-options-20261117/`, including the initial compile-red test attempt. No new cloud/native-library run was performed: this change validates option plumbing and shared caption behavior, not new media-engine behavior.

Earlier live evidence, failures and broad core-agent race limitations remain unchanged. The native build still requires the existing tag and pinned library; default build/transport selection is unchanged.
