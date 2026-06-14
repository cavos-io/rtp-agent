from common import *  # noqa: F403

def stt_value_objects(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "speech_data_metadata")
    module = load_reference_stt()
    if action == "metadata_defaults":
        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(streaming=False, interim_results=False)
                )
                self.prewarm_calls = 0

            async def _recognize_impl(self, buffer: Any, *, language: Any = None, conn_options: Any = None) -> Any:
                return None

            def prewarm(self) -> None:
                self.prewarm_calls += 1

        provider = ScenarioSTT()
        provider.prewarm()
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "metadata_defaults",
                    "model": provider.model,
                    "provider": provider.provider,
                    "prewarm_calls": provider.prewarm_calls,
                }
            ],
        }
    if action == "multi_speaker_metadata":
        multi_speaker_module = load_reference_stt_multi_speaker()

        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(
                        streaming=True,
                        interim_results=True,
                        diarization=True,
                    )
                )

            @property
            def model(self) -> str:
                return "wrapped-model"

            @property
            def provider(self) -> str:
                return "wrapped-provider"

            async def _recognize_impl(
                self, buffer: Any, *, language: Any = None, conn_options: Any = None
            ) -> Any:
                return None

        wrapped = ScenarioSTT()
        adapter = multi_speaker_module.MultiSpeakerAdapter(stt=wrapped)
        return {
            "contract": "stt-multi-speaker-metadata",
            "events": [
                {
                    "name": "multi_speaker_metadata",
                    "model": adapter.model,
                    "provider": adapter.provider,
                    "wrapped_model": wrapped.model,
                    "wrapped_provider": wrapped.provider,
                    "diarization": adapter.capabilities.diarization,
                }
            ],
        }
    if action == "multi_speaker_prewarm":
        multi_speaker_module = load_reference_stt_multi_speaker()

        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(
                        streaming=True,
                        interim_results=True,
                        diarization=True,
                    )
                )
                self.prewarm_calls = 0

            def prewarm(self) -> None:
                self.prewarm_calls += 1

            async def _recognize_impl(
                self, buffer: Any, *, language: Any = None, conn_options: Any = None
            ) -> Any:
                return None

        wrapped = ScenarioSTT()
        adapter = multi_speaker_module.MultiSpeakerAdapter(stt=wrapped)
        adapter.prewarm()
        return {
            "contract": "stt-multi-speaker-prewarm",
            "events": [
                {
                    "name": "multi_speaker_prewarm",
                    "wrapped_prewarm_calls": wrapped.prewarm_calls,
                    "adapter_capability_diarization": adapter.capabilities.diarization,
                }
            ],
        }
    if action == "metrics_panic_isolated":
        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(streaming=False, interim_results=False)
                )

            async def _recognize_impl(self, buffer: Any, *, language: Any = None, conn_options: Any = None) -> Any:
                return None

        provider = ScenarioSTT()
        received_request_ids: list[str] = []

        def failing_handler(metrics: Any) -> None:
            raise RuntimeError("metrics handler failed")

        def recording_handler(metrics: Any) -> None:
            received_request_ids.append(metrics.request_id)

        provider.on("metrics_collected", failing_handler)
        provider.on("metrics_collected", recording_handler)
        escaped_error = False
        metrics = type("Metrics", (), {"request_id": "req"})()
        try:
            provider.emit("metrics_collected", metrics)
        except RuntimeError:
            escaped_error = True
        return {
            "contract": "stt-metrics-reference-panic-isolated",
            "events": [
                {
                    "name": "metrics_panic_isolated",
                    "escaped_error": escaped_error,
                    "handler_call_count": len(received_request_ids),
                    "request_ids": received_request_ids,
                }
            ],
        }
    if action == "metrics_unsubscribe":
        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(streaming=False, interim_results=False)
                )

            async def _recognize_impl(self, buffer: Any, *, language: Any = None, conn_options: Any = None) -> Any:
                return None

        provider = ScenarioSTT()
        request_ids: list[str] = []

        def handler(metrics: Any) -> None:
            request_ids.append(metrics.request_id)

        provider.on("metrics_collected", handler)
        provider.off("metrics_collected", handler)
        provider.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "after-unsubscribe"})(),
        )
        return {
            "contract": "stt-metrics-reference-unsubscribe",
            "events": [
                {
                    "name": "metrics_unsubscribe",
                    "request_ids": request_ids,
                }
            ],
        }
    if action == "error_panic_isolated":
        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(streaming=False, interim_results=False)
                )

            async def _recognize_impl(self, buffer: Any, *, language: Any = None, conn_options: Any = None) -> Any:
                return None

        provider = ScenarioSTT()
        received_labels: list[str] = []

        def failing_handler(error: Any) -> None:
            raise RuntimeError("error handler failed")

        def recording_handler(error: Any) -> None:
            received_labels.append(error.label)

        provider.on("error", failing_handler)
        provider.on("error", recording_handler)
        escaped_error = False
        err = type("Error", (), {"label": "provider.STT"})()
        try:
            provider.emit("error", err)
        except RuntimeError:
            escaped_error = True
        return {
            "contract": "stt-error-reference-panic-isolated",
            "events": [
                {
                    "name": "error_panic_isolated",
                    "escaped_error": escaped_error,
                    "handler_call_count": len(received_labels),
                    "labels": received_labels,
                }
            ],
        }
    if action == "error_unsubscribe":
        class ScenarioSTT(module.STT):
            def __init__(self) -> None:
                super().__init__(
                    capabilities=module.STTCapabilities(streaming=False, interim_results=False)
                )

            async def _recognize_impl(self, buffer: Any, *, language: Any = None, conn_options: Any = None) -> Any:
                return None

        provider = ScenarioSTT()
        labels: list[str] = []

        def handler(error: Any) -> None:
            labels.append(error.label)

        provider.on("error", handler)
        provider.off("error", handler)
        provider.emit("error", type("Error", (), {"label": "after-unsubscribe"})())
        return {
            "contract": "stt-error-reference-unsubscribe",
            "events": [
                {
                    "name": "error_unsubscribe",
                    "labels": labels,
                }
            ],
        }
    if action == "speech_data_metadata":
        word = load_reference_types().TimedString(
            "hello",
            start_time=0.1,
            end_time=0.4,
            confidence=0.95,
            start_time_offset=1.2,
            speaker_id="speaker-a",
        )
        data = module.SpeechData(
            language="en",
            text="hello",
            words=[word],
            source_languages=["en-US"],
            source_texts=["hello"],
            target_languages=["es"],
            target_texts=["hola"],
            metadata={"provider": "test"},
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "speech_data_metadata",
                    "language": str(data.language),
                    "text": data.text,
                    "word_text": str(data.words[0]),
                    "word_start": data.words[0].start_time,
                    "word_end": data.words[0].end_time,
                    "word_confidence": data.words[0].confidence,
                    "word_offset": data.words[0].start_time_offset,
                    "word_speaker": data.words[0].speaker_id,
                    "source_language": str(data.source_languages[0]),
                    "source_text": data.source_texts[0],
                    "target_language": str(data.target_languages[0]),
                    "target_text": data.target_texts[0],
                    "metadata": data.metadata["provider"],
                }
            ],
        }
    if action == "speech_data_optional_speaker":
        word = load_reference_types().TimedString("hello")
        data = module.SpeechData(language="en", text="hello", words=[word])
        return {
            "contract": "stt-speech-data-optional-speaker",
            "events": [
                {
                    "name": "speech_data_optional_speaker",
                    "speaker_id": data.speaker_id,
                    "speaker_is_none": data.speaker_id is None,
                    "word_speaker_id": data.words[0].speaker_id,
                    "word_speaker_is_none": data.words[0].speaker_id is None,
                }
            ],
        }
    if action == "speech_data_required_fields":
        required_fields = ["language", "text"]
        base = {"language": "", "text": ""}
        missing_fields = []
        for field_name in required_fields:
            kwargs = dict(base)
            del kwargs[field_name]
            try:
                module.SpeechData(**kwargs)
            except TypeError as exc:
                if field_name in str(exc):
                    missing_fields.append(field_name)
        data = module.SpeechData(**base)
        return {
            "contract": "stt-speech-data-required-fields",
            "events": [
                {
                    "name": "speech_data_required_fields",
                    "missing_fields": missing_fields,
                    "language": str(data.language),
                    "text": data.text,
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
            "contract": "stt-value-objects",
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
            "contract": "stt-timed-string-required-text",
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
            "contract": "stt-value-objects",
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
    if action == "speech_event_usage":
        event = module.SpeechEvent(
            type=module.SpeechEventType.RECOGNITION_USAGE,
            request_id="req-1",
            recognition_usage=module.RecognitionUsage(
                audio_duration=1.25,
                input_tokens=3,
                output_tokens=5,
            ),
            speech_start_time=42.5,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "speech_event_usage",
                    "type": event.type.value,
                    "request_id": event.request_id,
                    "audio_duration": event.recognition_usage.audio_duration,
                    "input_tokens": event.recognition_usage.input_tokens,
                    "output_tokens": event.recognition_usage.output_tokens,
                    "speech_start_time": event.speech_start_time,
                }
            ],
        }
    if action == "recognition_usage_required_duration":
        missing_field = ""
        try:
            module.RecognitionUsage(input_tokens=3, output_tokens=5)
        except TypeError as exc:
            if "audio_duration" in str(exc):
                missing_field = "audio_duration"
        zero = module.RecognitionUsage(audio_duration=0)
        return {
            "contract": "stt-recognition-usage-required-field",
            "events": [
                {
                    "name": "recognition_usage_required_duration",
                    "missing_required": missing_field == "audio_duration",
                    "missing_field": missing_field,
                    "zero_audio_duration": zero.audio_duration,
                    "zero_input_tokens": zero.input_tokens,
                    "zero_output_tokens": zero.output_tokens,
                }
            ],
        }
    if action == "speech_event_json_fields":
        word = load_reference_types().TimedString(
            "hello",
            start_time=1.0,
            end_time=2.0,
            confidence=0.9,
            start_time_offset=0.25,
            speaker_id="speaker-a",
        )
        alternative = module.SpeechData(
            language="en",
            text="hello",
            start_time=1.0,
            end_time=2.0,
            confidence=0.9,
            speaker_id="speaker-a",
            is_primary_speaker=True,
            words=[word],
            source_languages=["en-US"],
            source_texts=["hello"],
            target_languages=["es"],
            target_texts=["hola"],
            metadata={"provider": "test"},
        )
        event = module.SpeechEvent(
            type=module.SpeechEventType.RECOGNITION_USAGE,
            request_id="req-1",
            alternatives=[alternative],
            recognition_usage=module.RecognitionUsage(
                audio_duration=1.25,
                input_tokens=3,
                output_tokens=5,
            ),
            speech_start_time=12.5,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "speech_event_json_fields",
                    "type": event.type.value,
                    "request_id": event.request_id,
                    "speech_start_time": event.speech_start_time,
                    "has_recognition_usage": event.recognition_usage is not None,
                    "has_camel_case": False,
                    "has_target_only_interrupted": False,
                    "alternative_start_time": int(event.alternatives[0].start_time),
                    "alternative_end_time": int(event.alternatives[0].end_time),
                    "alternative_speaker_id": event.alternatives[0].speaker_id,
                    "alternative_is_primary_speaker": event.alternatives[0].is_primary_speaker,
                    "word_start_time_offset": event.alternatives[0].words[0].start_time_offset,
                    "word_speaker_id": event.alternatives[0].words[0].speaker_id,
                }
            ],
        }
    if action == "speech_event_empty_alternatives_marshal":
        event = module.SpeechEvent(type=module.SpeechEventType.END_OF_SPEECH)
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "speech_event_empty_alternatives_marshal",
                    "type": event.type.value,
                    "alternatives_is_list": isinstance(event.alternatives, list),
                    "alternatives_length": len(event.alternatives),
                }
            ],
        }
    if action == "speech_event_empty_alternatives_unmarshal":
        event = module.SpeechEvent(
            type=module.SpeechEventType.END_OF_SPEECH,
            request_id="req-1",
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "speech_event_empty_alternatives_unmarshal",
                    "type": event.type.value,
                    "request_id": event.request_id,
                    "alternatives_is_list": isinstance(event.alternatives, list),
                    "alternatives_length": len(event.alternatives),
                }
            ],
        }
    if action == "speech_event_required_type":
        missing_field = ""
        try:
            module.SpeechEvent(request_id="req-1")
        except TypeError as exc:
            if "type" in str(exc):
                missing_field = "type"
        event = module.SpeechEvent(
            type=module.SpeechEventType.END_OF_SPEECH,
            request_id="req-1",
        )
        return {
            "contract": "stt-speech-event-required-type",
            "events": [
                {
                    "name": "speech_event_required_type",
                    "missing_required": missing_field == "type",
                    "missing_field": missing_field,
                    "type": event.type.value,
                    "request_id": event.request_id,
                    "alternatives_is_list": isinstance(event.alternatives, list),
                    "alternatives_length": len(event.alternatives),
                }
            ],
        }
    if action == "stt_error_payload":
        err = module.STTError(
            type="stt_error",
            timestamp=1.0,
            label="provider.STT",
            error=Exception("provider disconnected"),
            recoverable=True,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "stt_error_payload",
                    "type": err.type,
                    "label": err.label,
                    "recoverable": err.recoverable,
                    "timestamp_positive": err.timestamp > 0,
                    "error_message": str(err.error),
                }
            ],
        }
    if action == "stt_error_json":
        err = module.STTError(
            type="stt_error",
            timestamp=1.0,
            label="provider.STT",
            error=Exception("provider disconnected"),
            recoverable=True,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "stt_error_json",
                    "type": err.type,
                    "label": err.label,
                    "recoverable": err.recoverable,
                    "timestamp_positive": err.timestamp > 0,
                    "has_error_field": False,
                    "has_err_field": False,
                }
            ],
        }
    if action == "stt_error_required_fields":
        required_fields = ["timestamp", "label", "recoverable"]
        base = {
            "timestamp": 1.25,
            "label": "provider.STT",
            "error": Exception("provider disconnected"),
            "recoverable": True,
        }
        accepted_missing_fields = []
        for field_name in required_fields:
            kwargs = dict(base)
            del kwargs[field_name]
            try:
                module.STTError(**kwargs)
                accepted_missing_fields.append(field_name)
            except Exception:
                pass
        err = module.STTError(**base)
        return {
            "contract": "stt-error-required-fields",
            "events": [
                {
                    "name": "stt_error_required_fields",
                    "accepted_missing_fields": accepted_missing_fields,
                    "type": err.type,
                    "timestamp": err.timestamp,
                    "label": err.label,
                    "recoverable": err.recoverable,
                }
            ],
        }
    if action == "capabilities_json":
        caps = module.STTCapabilities(
            streaming=True,
            interim_results=True,
            diarization=True,
            aligned_transcript="word",
            offline_recognize=True,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "capabilities_json",
                    "streaming": caps.streaming,
                    "interim_results": caps.interim_results,
                    "diarization": caps.diarization,
                    "aligned_transcript": caps.aligned_transcript,
                    "offline_recognize": caps.offline_recognize,
                    "has_camel_case": False,
                }
            ],
        }
    if action == "capabilities_missing_aligned":
        caps = module.STTCapabilities(streaming=True, interim_results=True)
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "capabilities_missing_aligned",
                    "aligned_transcript": caps.aligned_transcript,
                }
            ],
        }
    if action == "capabilities_unmarshal_defaults":
        caps = module.STTCapabilities(
            streaming=True,
            interim_results=True,
            diarization=False,
            aligned_transcript=False,
        )
        return {
            "contract": "stt-value-objects",
            "events": [
                {
                    "name": "capabilities_unmarshal_defaults",
                    "streaming": caps.streaming,
                    "interim_results": caps.interim_results,
                    "diarization": caps.diarization,
                    "aligned_transcript": "" if caps.aligned_transcript is False else caps.aligned_transcript,
                    "offline_recognize": caps.offline_recognize,
                }
            ],
        }
    if action == "capabilities_required_fields":
        required_fields = ["streaming", "interim_results"]
        base = {"streaming": True, "interim_results": True}
        missing_fields = []
        for field_name in required_fields:
            kwargs = dict(base)
            del kwargs[field_name]
            try:
                module.STTCapabilities(**kwargs)
            except TypeError as exc:
                if field_name in str(exc):
                    missing_fields.append(field_name)
        caps = module.STTCapabilities(**base)
        aligned = "" if caps.aligned_transcript is False else caps.aligned_transcript
        return {
            "contract": "stt-capabilities-required-fields",
            "events": [
                {
                    "name": "capabilities_required_fields",
                    "missing_fields": missing_fields,
                    "streaming": caps.streaming,
                    "interim_results": caps.interim_results,
                    "diarization": caps.diarization,
                    "aligned_transcript": aligned,
                    "offline_recognize": caps.offline_recognize,
                }
            ],
        }
    raise ValueError(f"unsupported STT value object action {action!r}")


def stt_fallback(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "metadata")
    fallback_module = load_reference_stt_fallback()
    stt_module = load_reference_stt()

    def aligned(value: Any) -> str:
        if value is False or value is None:
            return ""
        return str(value)

    class FakeSTT(stt_module.STT):
        def __init__(
            self,
            label: str,
            *,
            streaming: bool = True,
            interim_results: bool = False,
            diarization: bool = False,
            aligned_transcript: Any = False,
            offline_recognize: bool = True,
            recognize_error: bool = False,
        ) -> None:
            super().__init__(
                capabilities=stt_module.STTCapabilities(
                    streaming=streaming,
                    interim_results=interim_results,
                    diarization=diarization,
                    aligned_transcript=aligned_transcript,
                    offline_recognize=offline_recognize,
                )
            )
            self._label = label
            self._recognize_error = recognize_error
            self.recognize_calls = 0

        async def _recognize_impl(self, *args: Any, **kwargs: Any) -> Any:
            self.recognize_calls += 1
            if self._recognize_error:
                raise RuntimeError(f"{self._label} failed")
            return stt_module.SpeechEvent(type=stt_module.SpeechEventType.FINAL_TRANSCRIPT)

    def caps_event(name: str, adapter: Any) -> dict[str, Any]:
        caps = adapter.capabilities
        return {
            "name": name,
            "streaming": caps.streaming,
            "interim_results": caps.interim_results,
            "diarization": caps.diarization,
            "aligned_transcript": aligned(caps.aligned_transcript),
            "offline_recognize": caps.offline_recognize,
        }

    if action == "metadata":
        adapter = fallback_module.FallbackAdapter([FakeSTT("primary")])
        return {
            "contract": "stt-fallback",
            "events": [
                {
                    "name": "metadata",
                    "model": adapter.model,
                    "provider": adapter.provider,
                }
            ],
        }
    if action == "option_defaults":
        adapter = fallback_module.FallbackAdapter([FakeSTT("primary")])
        return {
            "contract": "stt-fallback",
            "events": [
                {
                    "name": "option_defaults",
                    "max_retry_per_stt": adapter._max_retry_per_stt,
                    "attempt_timeout_seconds": int(adapter._attempt_timeout),
                    "retry_interval_seconds": int(adapter._retry_interval),
                }
            ],
        }
    if action == "provider_error_not_forwarded":
        primary = FakeSTT("primary")
        fallback = FakeSTT("fallback")
        adapter = fallback_module.FallbackAdapter([primary, fallback])
        labels: list[str] = []
        adapter.on("error", lambda error: labels.append(error.label))
        primary.emit("error", type("Error", (), {"label": "primary"})())
        fallback.emit("error", type("Error", (), {"label": "fallback"})())
        adapter.emit("error", type("Error", (), {"label": "adapter"})())
        return {
            "contract": "stt-fallback-provider-error-not-forwarded",
            "events": [
                {"name": "provider_error_not_forwarded", "labels": labels}
            ],
        }
    if action == "error_unsubscribe_local":
        primary = FakeSTT("primary")
        adapter = fallback_module.FallbackAdapter([primary])
        labels: list[str] = []

        def handler(error: Any) -> None:
            labels.append(error.label)

        adapter.on("error", handler)
        adapter.off("error", handler)
        primary.emit("error", type("Error", (), {"label": "primary"})())
        adapter.emit("error", type("Error", (), {"label": "adapter"})())
        return {
            "contract": "stt-fallback-error-unsubscribe-local",
            "events": [
                {"name": "error_unsubscribe_local", "labels": labels}
            ],
        }
    if action == "forward_metrics":
        primary = FakeSTT("primary")
        fallback = FakeSTT("fallback")
        adapter = fallback_module.FallbackAdapter([primary, fallback])
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        primary.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "primary-req"})(),
        )
        fallback.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "fallback-req"})(),
        )
        return {
            "contract": "stt-fallback-forward-provider-metrics",
            "events": [
                {"name": "forward_metrics", "request_ids": request_ids}
            ],
        }
    if action == "metrics_unsubscribe":
        primary = FakeSTT("primary")
        adapter = fallback_module.FallbackAdapter([primary])
        request_ids: list[str] = []

        def handler(metrics: Any) -> None:
            request_ids.append(metrics.request_id)

        adapter.on("metrics_collected", handler)
        primary.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "before"})(),
        )
        adapter.off("metrics_collected", handler)
        primary.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "provider-after"})(),
        )
        adapter.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "local-after"})(),
        )
        return {
            "contract": "stt-fallback-metrics-unsubscribe",
            "events": [
                {"name": "metrics_unsubscribe", "request_ids": request_ids}
            ],
        }
    if action == "close_unsubscribes_provider_metrics":
        primary = FakeSTT("primary")
        adapter = fallback_module.FallbackAdapter([primary])
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        asyncio.run(adapter.aclose())
        primary.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "late"})(),
        )
        return {
            "contract": "stt-fallback-close-unsubscribes-provider-metrics",
            "events": [
                {"name": "close_unsubscribes_provider_metrics", "request_ids": request_ids}
            ],
        }
    if action == "availability_panic_isolated":
        primary = FakeSTT("primary", recognize_error=True)
        fallback = FakeSTT("fallback")
        adapter = fallback_module.FallbackAdapter(
            [primary, fallback],
            max_retry_per_stt=0,
        )
        received_count = 0
        escaped_error = False

        def bad_handler(event: Any) -> None:
            raise RuntimeError("availability handler failed")

        def good_handler(event: Any) -> None:
            nonlocal received_count
            if event.stt is primary and event.available is False:
                received_count += 1

        adapter.on("stt_availability_changed", bad_handler)
        adapter.on("stt_availability_changed", good_handler)

        async def run_recognize() -> None:
            await adapter.recognize([])

        try:
            asyncio.run(run_recognize())
        except RuntimeError:
            escaped_error = True
        return {
            "contract": "stt-fallback-availability-panic-isolated",
            "events": [
                {
                    "name": "availability_panic_isolated",
                    "received_count": received_count,
                    "escaped_error": escaped_error,
                }
            ],
        }
    if action == "availability_unsubscribe":
        primary = FakeSTT("primary", recognize_error=True)
        fallback = FakeSTT("fallback")
        adapter = fallback_module.FallbackAdapter(
            [primary, fallback],
            max_retry_per_stt=0,
        )
        received_count = 0

        def handler(event: Any) -> None:
            nonlocal received_count
            received_count += 1

        adapter.on("stt_availability_changed", handler)
        adapter.off("stt_availability_changed", handler)

        async def run_recognize() -> None:
            await adapter.recognize([])

        asyncio.run(run_recognize())
        return {
            "contract": "stt-fallback-availability-unsubscribe",
            "events": [
                {
                    "name": "availability_unsubscribe",
                    "received_count": received_count,
                }
            ],
        }
    if action == "all_failed_recognize":
        if not hasattr(fallback_module.logger, "debug"):
            fallback_module.logger.debug = lambda *args, **kwargs: None
        if not hasattr(fallback_module.logger, "exception"):
            fallback_module.logger.exception = lambda *args, **kwargs: None
        primary = FakeSTT("primary", recognize_error=True)
        fallback = FakeSTT("fallback", recognize_error=True)
        adapter = fallback_module.FallbackAdapter(
            [primary, fallback],
            max_retry_per_stt=0,
        )
        error_class = ""
        retryable = False

        async def run_recognize() -> None:
            await adapter.recognize([])

        try:
            asyncio.run(run_recognize())
        except Exception as exc:
            error_class = type(exc).__name__
            retryable = getattr(exc, "retryable", False)

        return {
            "contract": "stt-fallback-all-failed-recognize",
            "events": [
                {
                    "name": "all_failed_recognize",
                    "error_class": error_class,
                    "retryable": retryable,
                    "primary_calls": primary.recognize_calls,
                    "fallback_calls": fallback.recognize_calls,
                }
            ],
        }
    if action == "validation":
        mode = input_data.get("mode", "empty")
        try:
            if mode == "empty":
                fallback_module.FallbackAdapter([])
            elif mode == "nonstreaming":
                fallback_module.FallbackAdapter([FakeSTT("offline", streaming=False)])
            elif mode == "all_nonstreaming":
                fallback_module.FallbackAdapter(
                    [
                        FakeSTT("offline-a", streaming=False),
                        FakeSTT("offline-b", streaming=False),
                    ]
                )
            else:
                raise ValueError(f"unsupported STT fallback validation mode {mode!r}")
        except Exception as exc:
            return {
                "contract": "stt-fallback",
                "events": [
                    {
                        "name": f"validation_{mode}",
                        "error": True,
                        "error_class": type(exc).__name__,
                        "message": str(exc),
                    }
                ],
            }
        return {
            "contract": "stt-fallback",
            "events": [{"name": f"validation_{mode}", "error": False}],
        }
    if action == "capabilities":
        mode = input_data.get("mode", "aggregate")
        if mode == "aggregate":
            adapter = fallback_module.FallbackAdapter(
                [
                    FakeSTT(
                        "primary",
                        streaming=True,
                        interim_results=True,
                        diarization=True,
                        offline_recognize=False,
                    ),
                    FakeSTT(
                        "fallback",
                        streaming=True,
                        interim_results=False,
                        diarization=False,
                        offline_recognize=True,
                    ),
                ]
            )
        elif mode == "offline_advertised":
            adapter = fallback_module.FallbackAdapter(
                [
                    FakeSTT("primary", streaming=True, offline_recognize=False),
                    FakeSTT("fallback", streaming=True, offline_recognize=False),
                ]
            )
        elif mode == "aligned_primary":
            adapter = fallback_module.FallbackAdapter(
                [
                    FakeSTT("primary", streaming=True, aligned_transcript="word"),
                    FakeSTT("fallback", streaming=True, aligned_transcript="chunk"),
                ]
            )
        elif mode == "aligned_cleared":
            adapter = fallback_module.FallbackAdapter(
                [
                    FakeSTT("primary", streaming=True, aligned_transcript="word"),
                    FakeSTT("fallback", streaming=True, aligned_transcript=False),
                ]
            )
        else:
            raise ValueError(f"unsupported STT fallback capabilities mode {mode!r}")
        return {"contract": "stt-fallback", "events": [caps_event(f"capabilities_{mode}", adapter)]}
    if action == "vad_wrap":
        class FakeVAD:
            pass

        adapter = fallback_module.FallbackAdapter(
            [FakeSTT("offline", streaming=False, offline_recognize=True)],
            vad=FakeVAD(),
        )
        return {"contract": "stt-fallback", "events": [caps_event("vad_wrap", adapter)]}
    raise ValueError(f"unsupported STT fallback action {action!r}")


def stt_stream_adapter(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "capabilities")
    stream_adapter_module = load_reference_stt_stream_adapter()
    stt_module = load_reference_stt()

    class FakeSTT(stt_module.STT):
        def __init__(
            self,
            label: str,
            model: str = "",
            provider: str = "",
            texts: list[str] | None = None,
        ) -> None:
            super().__init__(
                capabilities=stt_module.STTCapabilities(
                    streaming=False,
                    interim_results=False,
                    diarization=False,
                    offline_recognize=True,
                )
            )
            self._label = label
            self._model = model
            self._provider = provider
            self._texts = texts or ["empty vad speech"]
            self.recognize_calls = 0
            self.recognize_frame_counts: list[int] = []

        @property
        def model(self) -> str:
            return self._model or "unknown"

        @property
        def provider(self) -> str:
            return self._provider or "unknown"

        async def _recognize_impl(self, *args: Any, **kwargs: Any) -> Any:
            buffer = kwargs.get("buffer")
            if buffer is None and args:
                buffer = args[0]
            self.recognize_calls += 1
            self.recognize_frame_counts.append(len(buffer) if isinstance(buffer, list) else 1)
            return stt_module.SpeechEvent(
                type=stt_module.SpeechEventType.FINAL_TRANSCRIPT,
                alternatives=[
                    stt_module.SpeechData(language="en", text=text) for text in self._texts
                ],
            )

    class FakeVAD:
        def __init__(self, events: list[Any] | None = None, next_error: Exception | None = None) -> None:
            self._events = events or []
            self._next_error = next_error
            self.last_stream: Any | None = None

        def stream(self) -> Any:
            self.last_stream = FakeVADStream(self._events, self._next_error)
            return self.last_stream

    class FakeVADStream:
        def __init__(self, events: list[Any], next_error: Exception | None = None) -> None:
            self._events = list(events)
            self._next_error = next_error
            self.closed = False
            self.close_calls = 0
            self.flush_calls = 0
            self.end_input_calls = 0

        def push_frame(self, frame: Any) -> None:
            pass

        def flush(self) -> None:
            self.flush_calls += 1

        def end_input(self) -> None:
            self.end_input_calls += 1

        async def aclose(self) -> None:
            self.closed = True
            self.close_calls += 1

        def __aiter__(self) -> Any:
            return self

        async def __anext__(self) -> Any:
            if self._next_error is not None:
                error = self._next_error
                self._next_error = None
                raise error
            if not self._events:
                raise StopAsyncIteration
            return self._events.pop(0)

    def install_stream_adapter_runtime_shims() -> None:
        aio_mod = sys.modules["livekit.agents.utils.aio"]

        class ScenarioChan:
            _sentinel = object()

            def __init__(self) -> None:
                self._queue: asyncio.Queue[Any] = asyncio.Queue()
                self.closed = False

            def __class_getitem__(cls, item: Any) -> type:
                return cls

            def send_nowait(self, item: Any) -> None:
                if self.closed:
                    raise RuntimeError("channel is closed")
                self._queue.put_nowait(item)

            def close(self) -> None:
                if not self.closed:
                    self.closed = True
                    self._queue.put_nowait(self._sentinel)

            def __aiter__(self) -> Any:
                return self

            async def __anext__(self) -> Any:
                item = await self._queue.get()
                if item is self._sentinel:
                    raise StopAsyncIteration
                return item

        class ScenarioTeeResult(tuple):
            def __new__(cls, iterators: list[Any], task: asyncio.Task[Any]) -> Any:
                obj = super().__new__(cls, iterators)
                obj._task = task
                return obj

            async def aclose(self) -> None:
                self._task.cancel()
                try:
                    await self._task
                except BaseException:
                    pass

        class ScenarioTeeIterator:
            def __init__(self, queue: asyncio.Queue[Any], sentinel: object) -> None:
                self._queue = queue
                self._sentinel = sentinel

            def __aiter__(self) -> Any:
                return self

            async def __anext__(self) -> Any:
                item = await self._queue.get()
                if item is self._sentinel:
                    raise StopAsyncIteration
                return item

        class ScenarioItertools:
            @staticmethod
            def tee(source: Any, n: int) -> Any:
                sentinel = object()
                queues = [asyncio.Queue() for _ in range(n)]

                async def fanout() -> None:
                    async for item in source:
                        for queue in queues:
                            queue.put_nowait(item)
                    for queue in queues:
                        queue.put_nowait(sentinel)

                task = asyncio.create_task(fanout())
                return ScenarioTeeResult(
                    [ScenarioTeeIterator(queue, sentinel) for queue in queues], task
                )

        async def cancel_and_wait(*tasks: asyncio.Task[Any]) -> None:
            for task in tasks:
                task.cancel()
            for task in tasks:
                try:
                    await task
                except BaseException:
                    pass

        aio_mod.Chan = ScenarioChan
        aio_mod.itertools = ScenarioItertools
        aio_mod.cancel_and_wait = cancel_and_wait

    async def collect_stream_events(adapter: Any, limit: int = 2) -> list[Any]:
        stream = adapter.stream(language="en")
        events: list[Any] = []
        async for event in stream:
            events.append(event)
            if len(events) == limit:
                break
        await stream.aclose()
        return events

    def event_type_value(event: Any) -> str:
        return event.type.value if hasattr(event.type, "value") else str(event.type)

    async def wait_for(predicate: Any) -> None:
        for _ in range(20):
            if predicate():
                return
            await asyncio.sleep(0.001)

    if action == "capabilities":
        adapter = stream_adapter_module.StreamAdapter(stt=FakeSTT("wrapped"), vad=FakeVAD())
        caps = adapter.capabilities
        return {
            "contract": "stt-stream-adapter",
            "events": [
                {
                    "name": "capabilities",
                    "streaming": caps.streaming,
                    "interim_results": caps.interim_results,
                    "diarization": caps.diarization,
                    "aligned_transcript": ""
                    if caps.aligned_transcript is False
                    else caps.aligned_transcript,
                    "offline_recognize": caps.offline_recognize,
                }
            ],
        }
    if action == "wrapped":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        return {
            "contract": "stt-stream-adapter",
            "events": [
                {
                    "name": "wrapped",
                    "same_instance": adapter.wrapped_stt is wrapped,
                    "wrapped_label": adapter.wrapped_stt.label,
                }
            ],
        }
    if action == "public_wrapper":
        wrapper_cls = stream_adapter_module.StreamAdapterWrapper
        is_recognize_stream = issubclass(
            wrapper_cls, stt_module.RecognizeStream
        ) or all(
            hasattr(wrapper_cls, name)
            for name in ("push_frame", "flush", "aclose", "__aiter__")
        )
        return {
            "contract": "stt-stream-adapter",
            "events": [
                {
                    "name": "public_wrapper",
                    "type_name": wrapper_cls.__name__,
                    "is_recognize_stream": is_recognize_stream,
                    "has_push_frame": hasattr(wrapper_cls, "push_frame"),
                    "has_flush": hasattr(wrapper_cls, "flush"),
                    "has_end_input": hasattr(wrapper_cls, "end_input"),
                    "has_start_time_offset": hasattr(wrapper_cls, "start_time_offset"),
                    "has_start_time": hasattr(wrapper_cls, "start_time"),
                }
            ],
        }
    if action == "forward_metrics":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        wrapped.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "req-1"})(),
        )
        return {
            "contract": "stt-stream-adapter",
            "events": [
                {
                    "name": "forward_metrics",
                    "request_ids": request_ids,
                    "count": len(request_ids),
                }
            ],
        }
    if action == "close_unsubscribes_provider_metrics":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        request_ids: list[str] = []
        adapter.on(
            "metrics_collected",
            lambda metrics: request_ids.append(metrics.request_id),
        )
        wrapped.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "before"})(),
        )
        asyncio.run(adapter.aclose())
        wrapped.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "after"})(),
        )
        adapter.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "local"})(),
        )
        return {
            "contract": "stt-stream-adapter-close-unsubscribes-provider-metrics",
            "events": [
                {"name": "close_unsubscribes_provider_metrics", "request_ids": request_ids}
            ],
        }
    if action == "metrics_unsubscribe":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        request_ids: list[str] = []

        def handler(metrics: Any) -> None:
            request_ids.append(metrics.request_id)

        adapter.on("metrics_collected", handler)
        wrapped.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "before"})(),
        )
        adapter.off("metrics_collected", handler)
        wrapped.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "provider-after"})(),
        )
        adapter.emit(
            "metrics_collected",
            type("Metrics", (), {"request_id": "local-after"})(),
        )
        return {
            "contract": "stt-stream-adapter-metrics-unsubscribe",
            "events": [
                {"name": "metrics_unsubscribe", "request_ids": request_ids}
            ],
        }
    if action == "provider_error_not_forwarded":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        labels: list[str] = []
        adapter.on("error", lambda error: labels.append(error.label))
        wrapped.emit(
            "error",
            type("Error", (), {"label": "wrapped"})(),
        )
        adapter.emit(
            "error",
            type("Error", (), {"label": "adapter"})(),
        )
        return {
            "contract": "stt-stream-adapter-provider-error-not-forwarded",
            "events": [
                {"name": "provider_error_not_forwarded", "labels": labels}
            ],
        }
    if action == "error_unsubscribe_local":
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD())
        labels: list[str] = []

        def handler(error: Any) -> None:
            labels.append(error.label)

        adapter.on("error", handler)
        adapter.off("error", handler)
        wrapped.emit("error", type("Error", (), {"label": "wrapped"})())
        adapter.emit("error", type("Error", (), {"label": "adapter"})())
        return {
            "contract": "stt-stream-adapter-error-unsubscribe-local",
            "events": [
                {"name": "error_unsubscribe_local", "labels": labels}
            ],
        }
    if action == "metadata":
        adapter = stream_adapter_module.StreamAdapter(
            stt=FakeSTT("wrapped", model="wrapped-model", provider="wrapped-provider"),
            vad=FakeVAD(),
        )
        return {
            "contract": "stt-stream-adapter",
            "events": [
                {
                    "name": "metadata",
                    "model": adapter.model,
                    "provider": adapter.provider,
                }
            ],
        }
    if action == "empty_vad_end":
        install_stream_adapter_runtime_shims()
        wrapped = FakeSTT("wrapped")
        vad_event = type(
            "VADEvent",
            (),
            {"type": "end_of_speech", "frames": []},
        )()
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD([vad_event]))
        events = asyncio.run(collect_stream_events(adapter))
        return {
            "contract": "stt-stream-adapter-empty-vad-end",
            "events": [
                {
                    "name": "empty_vad_end",
                    "event_types": [event_type_value(event) for event in events],
                    "final_text": events[-1].alternatives[0].text if events[-1].alternatives else "",
                    "recognize_calls": wrapped.recognize_calls,
                    "recognize_frame_counts": wrapped.recognize_frame_counts,
                }
            ],
        }
    if action == "first_alternative":
        install_stream_adapter_runtime_shims()
        wrapped = FakeSTT("wrapped", texts=["first", "second"])
        vad_event = type(
            "VADEvent",
            (),
            {"type": "end_of_speech", "frames": [object()]},
        )()
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=FakeVAD([vad_event]))
        events = asyncio.run(collect_stream_events(adapter))
        final_event = events[-1]
        return {
            "contract": "stt-stream-adapter-first-alternative",
            "events": [
                {
                    "name": "first_alternative",
                    "event_types": [event_type_value(event) for event in events],
                    "final_text": final_event.alternatives[0].text
                    if final_event.alternatives
                    else "",
                    "final_alternative_count": len(final_event.alternatives),
                    "recognize_calls": wrapped.recognize_calls,
                    "recognize_frame_counts": wrapped.recognize_frame_counts,
                }
            ],
        }
    if action == "forwards_flush":
        install_stream_adapter_runtime_shims()
        vad = FakeVAD()
        wrapped = FakeSTT("wrapped")
        adapter = stream_adapter_module.StreamAdapter(stt=wrapped, vad=vad)

        async def run_flush() -> dict[str, Any]:
            stream = adapter.stream(language="en")
            stream.flush()
            await wait_for(lambda: vad.last_stream is not None and vad.last_stream.flush_calls > 0)
            flush_calls = vad.last_stream.flush_calls if vad.last_stream is not None else 0
            end_input_calls = (
                vad.last_stream.end_input_calls if vad.last_stream is not None else 0
            )
            await stream.aclose()
            return {
                "flush_calls": flush_calls,
                "end_input_calls": end_input_calls,
                "recognize_calls": wrapped.recognize_calls,
            }

        result = asyncio.run(run_flush())
        return {
            "contract": "stt-stream-adapter-forwards-flush",
            "events": [
                {
                    "name": "forwards_flush",
                    **result,
                }
            ],
        }
    if action == "end_input_lifecycle":
        install_stream_adapter_runtime_shims()
        vad = FakeVAD()
        adapter = stream_adapter_module.StreamAdapter(stt=FakeSTT("wrapped"), vad=vad)

        async def run_end_input() -> dict[str, Any]:
            stream = adapter.stream(language="en")
            stream.end_input()
            await wait_for(
                lambda: vad.last_stream is not None and vad.last_stream.end_input_calls > 0
            )
            flush_calls = vad.last_stream.flush_calls if vad.last_stream is not None else 0
            end_input_calls = (
                vad.last_stream.end_input_calls if vad.last_stream is not None else 0
            )
            push_after_error = False
            flush_after_error = False
            second_end_error = False
            try:
                stream.push_frame(object())
            except Exception:
                push_after_error = True
            try:
                stream.flush()
            except Exception:
                flush_after_error = True
            try:
                stream.end_input()
            except Exception:
                second_end_error = True
            await stream.aclose()
            return {
                "flush_calls": flush_calls,
                "end_input_calls": end_input_calls,
                "push_after_error": push_after_error,
                "flush_after_error": flush_after_error,
                "second_end_error": second_end_error,
            }

        result = asyncio.run(run_end_input())
        return {
            "contract": "stt-stream-adapter-end-input-lifecycle",
            "events": [
                {
                    "name": "end_input_lifecycle",
                    **result,
                }
            ],
        }
    if action == "close_closes_vad":
        install_stream_adapter_runtime_shims()
        vad = FakeVAD()
        adapter = stream_adapter_module.StreamAdapter(stt=FakeSTT("wrapped"), vad=vad)

        async def run_close() -> dict[str, Any]:
            stream = adapter.stream(language="en")
            await wait_for(lambda: vad.last_stream is not None)
            await stream.aclose()
            return {
                "vad_stream_created": vad.last_stream is not None,
                "vad_closed": bool(vad.last_stream and vad.last_stream.closed),
                "close_calls": vad.last_stream.close_calls if vad.last_stream else 0,
            }

        result = asyncio.run(run_close())
        return {
            "contract": "stt-stream-adapter-close-closes-vad",
            "events": [
                {
                    "name": "close_closes_vad",
                    **result,
                }
            ],
        }
    if action == "vad_runtime_error":
        install_stream_adapter_runtime_shims()
        vad = FakeVAD(next_error=RuntimeError("vad failed"))
        adapter = stream_adapter_module.StreamAdapter(stt=FakeSTT("wrapped"), vad=vad)

        async def run_vad_runtime_error() -> dict[str, Any]:
            stream = adapter.stream(language="en")
            error_seen = False
            message_contains = False
            try:
                await stream.__anext__()
            except Exception as exc:
                error_seen = True
                message_contains = "vad failed" in str(exc)
            await stream.aclose()
            return {
                "error_seen": error_seen,
                "error_category": "runtime",
                "message_contains": message_contains,
                "vad_closed": bool(vad.last_stream and vad.last_stream.closed),
            }

        result = asyncio.run(run_vad_runtime_error())
        return {
            "contract": "stt-stream-adapter-vad-runtime-error",
            "events": [
                {
                    "name": "vad_runtime_error",
                    **result,
                }
            ],
        }
    raise ValueError(f"unsupported STT stream adapter action {action!r}")
