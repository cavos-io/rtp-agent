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

        async def _recognize_impl(self, *args: Any, **kwargs: Any) -> Any:
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
        def __init__(self, label: str, model: str = "", provider: str = "") -> None:
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

        @property
        def model(self) -> str:
            return self._model or "unknown"

        @property
        def provider(self) -> str:
            return self._provider or "unknown"

        async def _recognize_impl(self, *args: Any, **kwargs: Any) -> Any:
            return stt_module.SpeechEvent(type=stt_module.SpeechEventType.FINAL_TRANSCRIPT)

    class FakeVAD:
        pass

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
    raise ValueError(f"unsupported STT stream adapter action {action!r}")
