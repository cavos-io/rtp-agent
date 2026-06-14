from common import *  # noqa: F403
from collections.abc import AsyncIterable

def load_reference_text_transforms():
    base = repo_root() / "refs/agents/livekit-agents/livekit/agents/voice/transcription"
    voice_mod = sys.modules.get("livekit.agents.voice") or types.ModuleType("livekit.agents.voice")
    transcription_mod = sys.modules.get("livekit.agents.voice.transcription") or types.ModuleType(
        "livekit.agents.voice.transcription"
    )
    sys.modules["livekit.agents.voice"] = voice_mod
    sys.modules["livekit.agents.voice.transcription"] = transcription_mod

    filters_spec = importlib.util.spec_from_file_location(
        "livekit.agents.voice.transcription.filters", base / "filters.py"
    )
    if filters_spec is None or filters_spec.loader is None:
        raise RuntimeError("cannot load reference transcription filters.py")
    filters_module = importlib.util.module_from_spec(filters_spec)
    sys.modules["livekit.agents.voice.transcription.filters"] = filters_module
    filters_spec.loader.exec_module(filters_module)

    transforms_spec = importlib.util.spec_from_file_location(
        "livekit.agents.voice.transcription.text_transforms",
        base / "text_transforms.py",
    )
    if transforms_spec is None or transforms_spec.loader is None:
        raise RuntimeError("cannot load reference transcription text_transforms.py")
    transforms_module = importlib.util.module_from_spec(transforms_spec)
    sys.modules["livekit.agents.voice.transcription.text_transforms"] = transforms_module
    transforms_spec.loader.exec_module(transforms_module)
    return transforms_module


async def collect_text_transform_chunks(module: Any, chunks: list[str], transforms: list[str]) -> list[str]:
    async def source() -> AsyncIterable[str]:
        for chunk in chunks:
            yield chunk

    return [chunk async for chunk in module._apply_text_transforms(source(), transforms)]


def tts_stream_adapter(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "metadata")
    module = load_reference_tts_stream_adapter()

    class ScenarioTTS(module.TTS):
        def __init__(self) -> None:
            super().__init__(
                capabilities=module.TTSCapabilities(streaming=False),
                sample_rate=24000,
                num_channels=1,
            )
            self.prewarm_calls = 0
            self.close_calls = 0

        @property
        def model(self) -> str:
            return "voice-model"

        @property
        def provider(self) -> str:
            return "voice-provider"

        def synthesize(self, text: str, *, conn_options: Any = None) -> Any:
            return None

        def prewarm(self) -> None:
            self.prewarm_calls += 1

        async def aclose(self) -> None:
            self.close_calls += 1

    provider = ScenarioTTS()
    adapter = module.StreamAdapter(tts=provider)

    if action == "metadata":
        return {
            "contract": "tts-stream-adapter",
            "events": [
                {
                    "name": "metadata",
                    "model": adapter.model,
                    "provider": adapter.provider,
                    "sample_rate": adapter.sample_rate,
                    "channels": adapter.num_channels,
                    "streaming": adapter.capabilities.streaming,
                    "aligned_transcript": adapter.capabilities.aligned_transcript,
                }
            ],
        }
    if action == "prewarm":
        adapter.prewarm()
        return {
            "contract": "tts-stream-adapter",
            "events": [{"name": "prewarm", "prewarm_calls": provider.prewarm_calls}],
        }
    if action == "close":
        asyncio.run(adapter.aclose())
        return {
            "contract": "tts-stream-adapter",
            "events": [{"name": "close", "close_calls": provider.close_calls}],
        }
    if action == "forward_metrics":
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "req-1"})(),
        )
        return {
            "contract": "tts-stream-adapter",
            "events": [
                {"name": "forward_metrics", "request_ids": request_ids, "count": len(request_ids)}
            ],
        }
    if action == "close_preserves_metrics_forwarding":
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "before"})(),
        )
        asyncio.run(adapter.aclose())
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "after"})(),
        )
        adapter.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "local"})(),
        )
        return {
            "contract": "tts-stream-adapter-close-preserves-metrics-forwarding",
            "events": [
                {"name": "close_preserves_metrics_forwarding", "request_ids": request_ids}
            ],
        }
    if action == "unsubscribe_metrics":
        request_ids: list[str] = []

        def on_metrics(metrics: Any) -> None:
            request_ids.append(metrics.request_id)

        adapter.on("metrics_collected", on_metrics)
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "before"})(),
        )
        adapter.off("metrics_collected", on_metrics)
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "provider"})(),
        )
        adapter.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "adapter"})(),
        )
        return {
            "contract": "tts-stream-adapter-metrics-unsubscribe",
            "events": [
                {"name": "unsubscribe_metrics", "request_ids": request_ids}
            ],
        }
    raise ValueError(f"unsupported TTS stream adapter action {action!r}")


def tts_value_objects(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "metadata_defaults")
    module = load_reference_tts()

    class ScenarioTTS(module.TTS):
        def synthesize(self, text: str, *, conn_options: Any = None) -> Any:
            return None

    tts = ScenarioTTS(
        capabilities=module.TTSCapabilities(streaming=False),
        sample_rate=24000,
        num_channels=1,
    )

    if action == "metadata_defaults":
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "metadata_defaults",
                    "model": tts.model,
                    "provider": tts.provider,
                    "sample_rate": tts.sample_rate,
                    "channels": tts.num_channels,
                    "streaming": tts.capabilities.streaming,
                }
            ],
        }
    if action == "prewarm_noop":
        tts.prewarm()
        return {
            "contract": "tts-value-objects",
            "events": [
                {"name": "prewarm_noop", "error": False},
            ],
        }
    if action == "close_noop":
        return {
            "contract": "tts-value-objects",
            "events": [
                {"name": "close_noop", "error": False},
            ],
        }
    if action == "capabilities_json":
        caps = module.TTSCapabilities(streaming=True, aligned_transcript=True)
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "capabilities_json",
                    "streaming": caps.streaming,
                    "aligned_transcript": caps.aligned_transcript,
                    "has_camel_case": False,
                }
            ],
        }
    if action == "capabilities_default_aligned":
        caps = module.TTSCapabilities(streaming=True)
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "capabilities_default_aligned",
                    "streaming": caps.streaming,
                    "aligned_transcript": caps.aligned_transcript,
                }
            ],
        }
    if action == "capabilities_required_streaming":
        missing_required = False
        try:
            module.TTSCapabilities(aligned_transcript=True)
        except TypeError as exc:
            missing_required = "streaming" in str(exc)
        caps = module.TTSCapabilities(streaming=True)
        return {
            "contract": "tts-capabilities-required-streaming",
            "events": [
                {
                    "name": "capabilities_required_streaming",
                    "missing_required": missing_required,
                    "streaming": caps.streaming,
                    "aligned_transcript": caps.aligned_transcript,
                }
            ],
        }
    if action == "synthesized_audio_json":
        audio = module.SynthesizedAudio(
            frame=None,
            request_id="req-a",
            is_final=True,
            segment_id="segment-a",
            delta_text="hello",
        )
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "synthesized_audio_json",
                    "frame_is_none": audio.frame is None,
                    "request_id": audio.request_id,
                    "is_final": audio.is_final,
                    "segment_id": audio.segment_id,
                    "delta_text": audio.delta_text,
                    "has_go_field_names": False,
                    "has_timed_transcript": False,
                }
            ],
        }
    if action == "synthesized_audio_required_fields":
        required_fields = ["frame", "request_id"]
        base = {"frame": None, "request_id": ""}
        missing_fields = []
        for field_name in required_fields:
            kwargs = dict(base)
            del kwargs[field_name]
            try:
                module.SynthesizedAudio(**kwargs)
            except TypeError as exc:
                if field_name in str(exc):
                    missing_fields.append(field_name)
        audio = module.SynthesizedAudio(**base)
        return {
            "contract": "tts-synthesized-audio-required-fields",
            "events": [
                {
                    "name": "synthesized_audio_required_fields",
                    "missing_fields": missing_fields,
                    "frame_is_none": audio.frame is None,
                    "request_id": audio.request_id,
                    "is_final": audio.is_final,
                    "segment_id": audio.segment_id,
                    "delta_text": audio.delta_text,
                }
            ],
        }
    if action == "timed_string_json":
        timed = load_reference_types().TimedString(
            "hello",
            start_time=0.25,
            end_time=0.5,
            confidence=0.875,
            start_time_offset=1.25,
            speaker_id="speaker-a",
        )
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "timed_string_json",
                    "text": str(timed),
                    "start_time": timed.start_time,
                    "end_time": timed.end_time,
                    "confidence": timed.confidence,
                    "start_time_offset": timed.start_time_offset,
                    "speaker_id": timed.speaker_id,
                    "has_go_field_names": False,
                }
            ],
        }
    if action == "timed_string_optional_speaker":
        timed = load_reference_types().TimedString("hello")
        return {
            "contract": "tts-timed-string-optional-speaker",
            "events": [
                {
                    "name": "timed_string_optional_speaker",
                    "text": str(timed),
                    "speaker_id": timed.speaker_id,
                    "speaker_is_none": timed.speaker_id is None,
                }
            ],
        }
    if action == "timed_string_text":
        timed = load_reference_types().TimedString(
            "hello",
            start_time=0.25,
            end_time=0.5,
            confidence=0.875,
            start_time_offset=1.25,
            speaker_id="speaker-a",
        )
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "timed_string_text",
                    "text": str(timed),
                    "repr_includes_metadata": "start_time" in repr(timed),
                }
            ],
        }
    if action == "timed_string_required_text":
        types_module = load_reference_types()
        missing_required = False
        try:
            types_module.TimedString()
        except TypeError as exc:
            missing_required = "text" in str(exc)
        timed = types_module.TimedString("hello")
        return {
            "contract": "tts-timed-string-required-text",
            "events": [
                {
                    "name": "timed_string_required_text",
                    "missing_required": missing_required,
                    "text": str(timed),
                    "start_time_default": 0
                    if timed.start_time is types_module.NOT_GIVEN
                    else timed.start_time,
                    "end_time_default": 0
                    if timed.end_time is types_module.NOT_GIVEN
                    else timed.end_time,
                    "confidence_default": 0
                    if timed.confidence is types_module.NOT_GIVEN
                    else timed.confidence,
                    "start_time_offset_default": 0
                    if timed.start_time_offset is types_module.NOT_GIVEN
                    else timed.start_time_offset,
                }
            ],
        }
    if action == "tts_error_payload":
        err = module.TTSError(
            type="tts_error",
            timestamp=1.0,
            label="tts",
            error=Exception("provider disconnected"),
            recoverable=True,
        )
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "tts_error_payload",
                    "type": err.type,
                    "label": err.label,
                    "recoverable": err.recoverable,
                    "timestamp_positive": err.timestamp > 0,
                    "error_message": str(err.error),
                }
            ],
        }
    if action == "tts_error_json":
        err = module.TTSError(
            type="tts_error",
            timestamp=1.0,
            label="provider.TTS",
            error=Exception("provider disconnected"),
            recoverable=True,
        )
        return {
            "contract": "tts-value-objects",
            "events": [
                {
                    "name": "tts_error_json",
                    "type": err.type,
                    "label": err.label,
                    "recoverable": err.recoverable,
                    "timestamp_positive": err.timestamp > 0,
                    "has_error_field": False,
                    "has_err_field": False,
                }
            ],
        }
    if action == "text_transform":
        transform_module = load_reference_text_transforms()
        chunks = [str(chunk) for chunk in input_data.get("chunks", [])]
        transforms = [str(transform) for transform in input_data.get("transforms", ["filter_markdown"])]
        output = asyncio.run(collect_text_transform_chunks(transform_module, chunks, transforms))
        return {
            "contract": "tts-text-transforms",
            "events": [
                {
                    "name": "text_transform",
                    "chunks": output,
                    "joined": "".join(output),
                }
            ],
        }
    if action == "text_replace":
        transform_module = load_reference_text_transforms()
        chunks = [str(chunk) for chunk in input_data.get("chunks", [])]
        replacements = {
            str(old): str(new)
            for old, new in input_data.get("replacements", {}).items()
        }
        case_sensitive = bool(input_data.get("case_sensitive", False))
        output = asyncio.run(
            collect_text_transform_chunks(
                transform_module,
                chunks,
                [transform_module.replace(replacements, case_sensitive=case_sensitive)],
            )
        )
        joined = "".join(output)
        return {
            "contract": "tts-text-replacements",
            "events": [
                {
                    "name": "text_replace",
                    "joined": joined,
                    "contains_original": any(old in joined for old in replacements),
                }
            ],
        }
    if action == "text_replace_words":
        tokenize_utils = load_python_utils_runner().load_reference_tokenize_utils()
        chunks = [str(chunk) for chunk in input_data.get("chunks", [])]
        replacements = {
            str(old): str(new)
            for old, new in input_data.get("replacements", {}).items()
        }

        async def source() -> AsyncIterable[str]:
            for chunk in chunks:
                yield chunk

        async def collect() -> list[str]:
            return [
                chunk
                async for chunk in tokenize_utils.replace_words(
                    text=source(),
                    replacements=replacements,
                )
            ]

        output = asyncio.run(collect())
        joined = "".join(output)
        return {
            "contract": "tts-text-replacements",
            "events": [
                {
                    "name": "text_replace_words",
                    "joined": joined,
                    "workflow_preserved": "workflow" in joined,
                    "substring_replaced": "workstream" in joined,
                    "punctuation_preserved": "stream," in joined,
                    "final_word_replaced": joined.endswith("stream!"),
                }
            ],
        }
    raise ValueError(f"unsupported TTS value object action {action!r}")


def tts_fallback(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "model_provider")
    module = load_reference_tts_fallback()

    class ScenarioTTS(module.TTS):
        def __init__(self, *, sample_rate: int = 24000, num_channels: int = 1) -> None:
            super().__init__(
                capabilities=module.TTSCapabilities(streaming=False),
                sample_rate=sample_rate,
                num_channels=num_channels,
            )
            self.prewarm_calls = 0

        def synthesize(self, text: str, *, conn_options: Any = None) -> Any:
            return None

        def prewarm(self) -> None:
            self.prewarm_calls += 1

    provider = ScenarioTTS()

    if action == "model_provider":
        adapter = module.FallbackAdapter([provider])
        return {
            "contract": "tts-fallback",
            "events": [
                {
                    "name": "model_provider",
                    "model": adapter.model,
                    "provider": adapter.provider,
                    "sample_rate": adapter.sample_rate,
                    "channels": adapter.num_channels,
                }
            ],
        }
    if action == "sample_rate":
        low = ScenarioTTS(sample_rate=16000)
        high = ScenarioTTS(sample_rate=48000)
        adapter = module.FallbackAdapter([low, high], sample_rate=24000)
        return {
            "contract": "tts-fallback",
            "events": [
                {
                    "name": "sample_rate",
                    "sample_rate": adapter.sample_rate,
                    "channels": adapter.num_channels,
                    "streaming": adapter.capabilities.streaming,
                }
            ],
        }
    if action == "prewarm":
        primary = ScenarioTTS()
        fallback = ScenarioTTS()
        adapter = module.FallbackAdapter([primary, fallback])
        adapter.prewarm()
        return {
            "contract": "tts-fallback",
            "events": [
                {
                    "name": "prewarm",
                    "primary_prewarm_calls": primary.prewarm_calls,
                    "fallback_prewarm_calls": fallback.prewarm_calls,
                }
            ],
        }
    if action == "validation":
        mode = input_data.get("mode", "empty")
        error = False
        message = ""
        try:
            if mode == "empty":
                module.FallbackAdapter([])
            elif mode == "mixed_channels":
                mono = ScenarioTTS(num_channels=1)
                stereo = ScenarioTTS(num_channels=2)
                module.FallbackAdapter([mono, stereo])
            else:
                raise ValueError(f"unsupported TTS fallback validation mode {mode!r}")
        except ValueError as exc:
            error = True
            message = str(exc)
        return {
            "contract": "tts-fallback",
            "events": [
                {
                    "name": "validation",
                    "mode": mode,
                    "error": error,
                    "error_class": "error" if error else "",
                    "message": message,
                }
            ],
        }
    raise ValueError(f"unsupported TTS fallback action {action!r}")
