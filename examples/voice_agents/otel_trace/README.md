# OpenTelemetry voice agent example

A Go counterpart to [LiveKit's Python tracing example](https://github.com/livekit/agents/blob/livekit-agents%401.8.8/examples/voice_agents/otel_trace.py). Kelly uses a speech pipeline with LLM, STT and TTS fallbacks. Alloy uses OpenAI realtime. Both expose simulated weather and a tool to transfer to the other agent.

## Run

From the repository root, provide your credentials through the environment or an untracked `.env` file:

```sh
export LIVEKIT_URL=wss://your-project.livekit.cloud
export LIVEKIT_API_KEY=your-key
export LIVEKIT_API_SECRET=your-secret
export OPENAI_API_KEY=your-openai-key
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318

nix develop -c go run ./examples/voice_agents/otel_trace start
```

Dispatch a job to the registered agent using your usual LiveKit client. The default agent name is `otel-trace-example` unless configured by the existing application environment. Ask Kelly for weather, ask to speak to Alloy, then ask Alloy to transfer back to Kelly. Weather is a deterministic example response, not a real forecast. Live provider calls incur the providers' normal usage charges.

The session uses Silero ONNX VAD with its default speech/silence thresholds, shared across handoffs and closed at job shutdown. Run from the repository root with `resources/models/silero_vad.onnx` available. ONNX Runtime must be installed (provided by the Nix shell); outside Nix, set `ONNXRUNTIME_LIB_PATH` if needed. Initialization errors fail the job instead of falling back to energy-based VAD.

The example reads the existing application configuration for primary LLM/STT/TTS models, language and voice. Optional example-specific overrides:

| Variable | Default |
| --- | --- |
| `RTP_AGENT_LLM_FALLBACK_MODEL` | `google/gemini-2.5-flash` |
| `RTP_AGENT_STT_FALLBACK_MODEL` | `cartesia/ink-whisper` |
| `RTP_AGENT_TTS_FALLBACK_MODEL` | `rime/coda` |
| `RTP_AGENT_TTS_FALLBACK_VOICE` | `astra` |
| `OPENAI_REALTIME_MODEL` | `gpt-realtime` |

Alloy uses the `alloy` voice. `OPENAI_API_KEY` is checked when transferring to Alloy; Kelly can run without it.

## Export destination

The exporter honors standard `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_HEADERS`, plus their `OTEL_EXPORTER_OTLP_TRACES_*` overrides. The base endpoint gets `/v1/traces` appended. A traces-specific endpoint is used as the complete URL.

For Langfuse, set the base endpoint to your host's `/api/public/otel` path and supply its Basic authentication header through `OTEL_EXPORTER_OTLP_HEADERS`. Generate the header outside the repository from your public and secret keys. Do not commit it. The example includes both `session.id` and `langfuse.session.id` using the job ID.

The example initializes one provider per server process and transfers ownership with `AgentServer.SetTracerProvider`. Jobs share its exporter and batching worker, with separate trace contexts and metadata supplied by `JobContext.SetTraceMetadata`. Jobs flush after teardown, callbacks and report upload; the CLI closes the server and shuts down the provider after job finalization. Applications embedding the server must call `AgentServer.Close(ctx)` after execution. Explicit `JobContext.SetTracerProvider` overrides remain job-owned. Custom trace providers replace the LiveKit Cloud trace exporter; this example exports traces to the configured OTLP backend.

## Inspect the trace

Look for `job_entrypoint → agent_session`, pipeline `agent_turn → llm_node/tts_node`, and tool `function_tool → weather_lookup`. Transfers add `update_agent`; Alloy replies add `realtime_inference`, with `realtime_metrics` for metrics arriving after inference ends. Provider attempts appear as `llm_request_run` or `tts_request_run`; a successful call does not force a fallback.

Input waits use the session root because Go starts the session asynchronously. STT reports usage and transcription timing on the user turn rather than emitting an STT span. Collected metrics are logged by the framework; the example also demonstrates subscribing to those events.

See [trace migration and content controls](../../../docs/features/python-trace-parity.md) for changed attribute names and redaction settings.

## Offline checks

```sh
nix develop -c go test ./examples/voice_agents/otel_trace
```

These checks use a local fake OTLP collector and exercise weather validation and both handoff directions. They do not call LiveKit, OpenAI or inference providers, and do not establish live Cloud rendering compatibility.
