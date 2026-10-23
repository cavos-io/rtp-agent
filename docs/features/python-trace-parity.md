# Python 1.8.8 trace migration

RTP Agent follows the span contracts in the vendored LiveKit Agents reference at `31c6026df14eebb71975c16ca6a422ce68644b71`. The implementation uses existing OpenTelemetry providers, exporters and job isolation. The [voice tracing example](../../examples/voice_agents/otel_trace/README.md) demonstrates pipeline, realtime and handoff traces.

## Attribute query changes

Update dashboards and queries to these names. Legacy names are not emitted alongside the new names.

| Previous key | Current key |
| --- | --- |
| `lk.participant_identity` | `lk.pii.participant_identity` |
| `lk.room_name` | `lk.pii.room_name` |
| `lk.user_input` | `lk.pii.user_input` |
| `lk.instructions` | `lk.pii.instructions` |
| `lk.chat_ctx` | `lk.pii.chat_ctx` |
| `lk.response.text` | `lk.pii.response.text` |
| `lk.response.function_calls` | `lk.pii.response.function_calls` |
| `lk.function_tool.arguments` | `lk.pii.function_tool.arguments` |
| `lk.function_tool.output` | `lk.pii.function_tool.output` |
| `lk.user_transcript` | `lk.pii.user_transcript` |

TTS input text already uses `lk.pii.input_text`. Speech IDs, model names, token counts and timing keys retain their existing names. STT no longer emits `stt_stream`; inspect `user_turn` transcription delay and speech usage instead.

## Content controls

Custom exporters receive framework span content by default, matching Python 1.8.8. To suppress it, set `LIVEKIT_TELEMETRY_ALLOW_PII=0` or pass an explicit option before starting the session:

```go
allowPII := false
err := job.SetTracerProviderWithOptions(provider, telemetry.TracingOptions{
    AllowPII: &allowPII,
})
```

The existing `SetTracerProvider(provider, metadata...)` remains available. Explicit `AllowPII` overrides its environment default. Project redaction overrides both and strips content for every framework destination, including LiveKit Cloud.

Filtering removes attributes with a dot-delimited `pii` segment, GenAI content attributes and content events. It suppresses exception stack traces and replaces exception messages/error status descriptions with `exception details redacted`. It preserves non-content metadata and usage. It does not scan strings for entities. Tag custom sensitive attributes with a `pii` segment.

`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=false` independently disables GenAI message, tool-content and instruction capture. It does not suppress `lk.pii.*` attributes by itself.

Filtering applies to framework-created spans, including calls through `telemetry.StartSpan` and `telemetry.TracerFromContext`. Spans created directly through an unrelated provider and content inserted by unrelated processors remain that integration's responsibility. These controls do not rewrite application logs or change metric names.

## Lifecycle and ownership

`session_start` covers synchronous transport/model initialization and entry hooks. Async participant/audio waits are children of `agent_session`: Go does not block startup until a participant joins. `wait_for_audio_track` ends on the first usable frame or teardown.

`eou_wait` records a user's endpointing wait. Later triggers rearm it; resumed speech and discarded turns close it with distinct outcomes. `eou_detection` records each prediction inside the wait. The audio model's existing cancellation behavior is preserved; superseded tracing ends without extending the old wait.

One speech handle owns one `agent_turn`, with a `generation` event for each step. Realtime usage belongs to `realtime_inference`; late usage creates `realtime_metrics` under the ended inference span. Correlation state is bounded and cleared on close.

Provider-owned retry instrumentation avoids an extra synthetic run around several attempts. OpenAI/Anthropic LLM retries and core fallback attempts are visible. Provider/server retries that Go cannot observe are not inferred.

Worker roots are created after job exporter configuration is known and backdated to observed dispatch timestamps. Missing stages are omitted. Shutdown spans remain open until teardown, user callbacks and uploads settle, and end before provider finalization. Custom trace routing remains exclusive. `JobContext.SetTracerProvider` transfers provider ownership to the job. `AgentServer.SetTracerProvider` instead shares a server-owned provider across jobs; job metadata stays isolated, and server shutdown follows job finalization. The CLI calls `AgentServer.Close(ctx)` automatically; embedding applications must call it after execution.

Python-only features without equivalent Go execution boundaries are excluded: AMD, Python event-loop stalls, dynamic keyterm detection, decision models, full activity pause/resume and SDK-wide RPC interception.
