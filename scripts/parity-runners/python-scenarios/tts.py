from common import *  # noqa: F403

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
