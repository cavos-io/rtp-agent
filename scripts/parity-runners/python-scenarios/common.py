import asyncio
import importlib.util
import json
import os
import re
import sys
import types
from pathlib import Path
from typing import Any


def repo_root() -> Path:
    return Path(__file__).resolve().parents[3]


def load_reference_misc():
    misc_path = repo_root() / "refs/agents/livekit-agents/livekit/agents/utils/misc.py"

    try:
        import typing_extensions

        if not hasattr(typing_extensions, "TypeIs"):
            class TypeIs:
                def __class_getitem__(cls, item):
                    return bool

            typing_extensions.TypeIs = TypeIs
    except ImportError:
        typing_extensions = types.ModuleType("typing_extensions")

        class TypeIs:
            def __class_getitem__(cls, item):
                return bool

        typing_extensions.TypeIs = TypeIs
        sys.modules["typing_extensions"] = typing_extensions

    livekit_mod = types.ModuleType("livekit")
    agents_mod = types.ModuleType("livekit.agents")
    utils_mod = types.ModuleType("livekit.agents.utils")
    types_mod = types.ModuleType("livekit.agents.types")

    class NotGiven:
        pass

    class NotGivenOr:
        def __class_getitem__(cls, item):
            return object

    types_mod.NOT_GIVEN = NotGiven()
    types_mod.NotGiven = NotGiven
    types_mod.NotGivenOr = NotGivenOr

    sys.modules.setdefault("livekit", livekit_mod)
    sys.modules.setdefault("livekit.agents", agents_mod)
    sys.modules.setdefault("livekit.agents.utils", utils_mod)
    sys.modules["livekit.agents.types"] = types_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.utils.misc", misc_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference misc.py from {misc_path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.misc"] = module
    spec.loader.exec_module(module)
    return module


def load_python_utils_runner():
    path = repo_root() / "scripts/parity-runners/python-utils.py"
    spec = importlib.util.spec_from_file_location("rtp_agent_parity_python_utils", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load python-utils.py from {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_reference_language():
    root = repo_root()
    language_data_path = root / "refs/agents/livekit-agents/livekit/agents/_language_data.py"
    language_path = root / "refs/agents/livekit-agents/livekit/agents/language.py"

    livekit_mod = types.ModuleType("livekit")
    agents_mod = types.ModuleType("livekit.agents")
    agents_mod.__path__ = [str(language_path.parent)]
    sys.modules.setdefault("livekit", livekit_mod)
    sys.modules.setdefault("livekit.agents", agents_mod)

    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")
    if not hasattr(pydantic_mod, "GetCoreSchemaHandler"):
        pydantic_mod.GetCoreSchemaHandler = object
    sys.modules["pydantic"] = pydantic_mod

    pydantic_core_mod = sys.modules.get("pydantic_core") or types.ModuleType("pydantic_core")
    core_schema_mod = sys.modules.get("pydantic_core.core_schema") or types.ModuleType(
        "pydantic_core.core_schema"
    )
    if not hasattr(pydantic_core_mod, "CoreSchema"):
        pydantic_core_mod.CoreSchema = object
    if not hasattr(core_schema_mod, "no_info_plain_validator_function"):
        core_schema_mod.no_info_plain_validator_function = lambda *args, **kwargs: None
    if not hasattr(core_schema_mod, "to_string_ser_schema"):
        core_schema_mod.to_string_ser_schema = lambda *args, **kwargs: None
    pydantic_core_mod.core_schema = core_schema_mod
    sys.modules["pydantic_core"] = pydantic_core_mod
    sys.modules["pydantic_core.core_schema"] = core_schema_mod

    data_spec = importlib.util.spec_from_file_location(
        "livekit.agents._language_data", language_data_path
    )
    if data_spec is None or data_spec.loader is None:
        raise RuntimeError(f"cannot load reference _language_data.py from {language_data_path}")
    data_module = importlib.util.module_from_spec(data_spec)
    sys.modules["livekit.agents._language_data"] = data_module
    data_spec.loader.exec_module(data_module)

    language_spec = importlib.util.spec_from_file_location(
        "livekit.agents.language", language_path
    )
    if language_spec is None or language_spec.loader is None:
        raise RuntimeError(f"cannot load reference language.py from {language_path}")
    language_module = importlib.util.module_from_spec(language_spec)
    sys.modules["livekit.agents.language"] = language_module
    language_spec.loader.exec_module(language_module)
    return language_module


def load_reference_types():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/types.py"

    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")
    if not hasattr(pydantic_mod, "GetCoreSchemaHandler"):
        pydantic_mod.GetCoreSchemaHandler = object
    sys.modules["pydantic"] = pydantic_mod

    pydantic_core_mod = sys.modules.get("pydantic_core") or types.ModuleType("pydantic_core")
    core_schema_mod = sys.modules.get("pydantic_core.core_schema") or types.ModuleType(
        "pydantic_core.core_schema"
    )
    if not hasattr(pydantic_core_mod, "CoreSchema"):
        pydantic_core_mod.CoreSchema = object
    if not hasattr(core_schema_mod, "is_instance_schema"):
        core_schema_mod.is_instance_schema = lambda *args, **kwargs: None
    pydantic_core_mod.core_schema = core_schema_mod
    sys.modules["pydantic_core"] = pydantic_core_mod
    sys.modules["pydantic_core.core_schema"] = core_schema_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.types", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference types.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.types"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_exceptions():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/_exceptions.py"

    spec = importlib.util.spec_from_file_location("livekit.agents._exceptions", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference _exceptions.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents._exceptions"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_stt():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/stt/stt.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    rtc_mod = sys.modules.get("livekit.rtc") or types.ModuleType("livekit.rtc")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    stt_pkg = sys.modules.get("livekit.agents.stt") or types.ModuleType("livekit.agents.stt")
    metrics_mod = sys.modules.get("livekit.agents.metrics") or types.ModuleType(
        "livekit.agents.metrics"
    )
    metrics_base_mod = types.ModuleType("livekit.agents.metrics.base")
    log_mod = sys.modules.get("livekit.agents.log") or types.ModuleType("livekit.agents.log")
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    aio_mod = sys.modules.get("livekit.agents.utils.aio") or types.ModuleType(
        "livekit.agents.utils.aio"
    )
    audio_mod = types.ModuleType("livekit.agents.utils.audio")

    class EventEmitter:
        def __class_getitem__(cls, item: Any) -> type:
            return cls

        def __init__(self) -> None:
            self._listeners: dict[str, list[Any]] = {}

        def on(self, event: str, callback: Any) -> None:
            self._listeners.setdefault(event, []).append(callback)

        def off(self, event: str, callback: Any) -> None:
            listeners = self._listeners.get(event, [])
            self._listeners[event] = [listener for listener in listeners if listener is not callback]

        def emit(self, event: str, *args: Any, **kwargs: Any) -> None:
            for listener in list(self._listeners.get(event, [])):
                try:
                    listener(*args, **kwargs)
                except Exception:
                    pass

    class AudioFrame:
        pass

    class Metadata:
        def __init__(self, **kwargs: Any) -> None:
            self.__dict__.update(kwargs)

    class STTMetrics:
        def __init__(self, **kwargs: Any) -> None:
            self.__dict__.update(kwargs)

    class Logger:
        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")
    if not hasattr(pydantic_mod, "BaseModel"):
        class BaseModel:
            def __init__(self, **kwargs: Any) -> None:
                for key, value in kwargs.items():
                    setattr(self, key, value)

        pydantic_mod.BaseModel = BaseModel
    if not hasattr(pydantic_mod, "ConfigDict"):
        pydantic_mod.ConfigDict = lambda *args, **kwargs: dict(*args, **kwargs)
    if not hasattr(pydantic_mod, "Field"):
        pydantic_mod.Field = lambda default=..., **kwargs: default
    sys.modules["pydantic"] = pydantic_mod

    rtc_mod.EventEmitter = EventEmitter
    rtc_mod.AudioFrame = AudioFrame
    livekit_mod.rtc = rtc_mod
    metrics_base_mod.Metadata = Metadata
    metrics_mod.STTMetrics = STTMetrics
    log_mod.logger = Logger()
    utils_mod.AudioBuffer = object
    utils_mod.aio = aio_mod
    utils_mod.is_given = lambda value: value is not getattr(load_reference_types(), "NOT_GIVEN")
    audio_mod.AudioBuffer = object
    audio_mod.calculate_audio_duration = lambda _buffer: 0.0

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.rtc"] = rtc_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.stt"] = stt_pkg
    sys.modules["livekit.agents.metrics"] = metrics_mod
    sys.modules["livekit.agents.metrics.base"] = metrics_base_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["livekit.agents.utils"] = utils_mod
    sys.modules["livekit.agents.utils.aio"] = aio_mod
    sys.modules["livekit.agents.utils.audio"] = audio_mod

    load_reference_types()
    load_reference_language()
    load_reference_exceptions()

    spec = importlib.util.spec_from_file_location("livekit.agents.stt.stt", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference stt.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.stt.stt"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_stt_fallback():
    stt_module = load_reference_stt()
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/stt/fallback_adapter.py"

    stt_pkg = sys.modules["livekit.agents.stt"]
    stt_pkg.STT = stt_module.STT
    stt_pkg.RecognizeStream = stt_module.RecognizeStream
    stt_pkg.SpeechEvent = stt_module.SpeechEvent
    stt_pkg.SpeechEventType = stt_module.SpeechEventType
    stt_pkg.STTCapabilities = stt_module.STTCapabilities

    class StreamAdapter(stt_module.STT):
        def __init__(self, stt: Any, vad: Any) -> None:
            super().__init__(
                capabilities=stt_module.STTCapabilities(
                    streaming=True,
                    interim_results=False,
                    diarization=False,
                    offline_recognize=True,
                )
            )
            self.wrapped_stt = stt
            self.vad = vad

        async def _recognize_impl(self, *args: Any, **kwargs: Any) -> Any:
            return await self.wrapped_stt._recognize_impl(*args, **kwargs)

    stt_pkg.StreamAdapter = StreamAdapter

    vad_mod = sys.modules.get("livekit.agents.vad") or types.ModuleType("livekit.agents.vad")

    class VAD:
        pass

    vad_mod.VAD = VAD
    sys.modules["livekit.agents.vad"] = vad_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.stt.fallback_adapter", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference stt fallback_adapter.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.stt.fallback_adapter"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_stt_stream_adapter():
    stt_module = load_reference_stt()
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/stt/stream_adapter.py"

    stt_pkg = sys.modules["livekit.agents.stt"]
    stt_pkg.STT = stt_module.STT
    stt_pkg.RecognizeStream = stt_module.RecognizeStream
    stt_pkg.SpeechEvent = stt_module.SpeechEvent
    stt_pkg.SpeechEventType = stt_module.SpeechEventType
    stt_pkg.STTCapabilities = stt_module.STTCapabilities

    vad_mod = sys.modules.get("livekit.agents.vad") or types.ModuleType("livekit.agents.vad")

    class VAD:
        pass

    class VADEventType:
        START_OF_SPEECH = "start_of_speech"
        END_OF_SPEECH = "end_of_speech"

    vad_mod.VAD = VAD
    vad_mod.VADEventType = VADEventType
    sys.modules["livekit.agents.vad"] = vad_mod

    utils_mod = sys.modules["livekit.agents.utils"]
    utils_mod.merge_frames = lambda frames: frames

    spec = importlib.util.spec_from_file_location("livekit.agents.stt.stream_adapter", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference stt stream_adapter.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.stt.stream_adapter"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_tts():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/tts/tts.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    rtc_mod = sys.modules.get("livekit.rtc") or types.ModuleType("livekit.rtc")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    tts_pkg = sys.modules.get("livekit.agents.tts") or types.ModuleType("livekit.agents.tts")
    metrics_mod = sys.modules.get("livekit.agents.metrics") or types.ModuleType(
        "livekit.agents.metrics"
    )
    metrics_base_mod = types.ModuleType("livekit.agents.metrics.base")
    log_mod = sys.modules.get("livekit.agents.log") or types.ModuleType("livekit.agents.log")
    telemetry_mod = sys.modules.get("livekit.agents.telemetry") or types.ModuleType(
        "livekit.agents.telemetry"
    )
    trace_types_mod = types.ModuleType("livekit.agents.telemetry.trace_types")
    tracer_mod = types.ModuleType("livekit.agents.telemetry.tracer")
    telemetry_utils_mod = types.ModuleType("livekit.agents.telemetry.utils")
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    aio_mod = sys.modules.get("livekit.agents.utils.aio") or types.ModuleType(
        "livekit.agents.utils.aio"
    )
    audio_mod = types.ModuleType("livekit.agents.utils.audio")
    codecs_mod = types.ModuleType("livekit.agents.utils.codecs")

    class EventEmitter:
        def __class_getitem__(cls, item: Any) -> type:
            return cls

        def __init__(self) -> None:
            self._listeners: dict[str, list[Any]] = {}

        def on(self, event: str, callback: Any) -> None:
            self._listeners.setdefault(event, []).append(callback)

        def off(self, event: str, callback: Any) -> None:
            listeners = self._listeners.get(event, [])
            self._listeners[event] = [listener for listener in listeners if listener is not callback]

        def emit(self, event: str, *args: Any, **kwargs: Any) -> None:
            for listener in list(self._listeners.get(event, [])):
                try:
                    listener(*args, **kwargs)
                except Exception:
                    continue

    class AudioFrame:
        pass

    class Metadata:
        def __init__(self, **kwargs: Any) -> None:
            self.__dict__.update(kwargs)

    class TTSMetrics:
        def __init__(self, **kwargs: Any) -> None:
            self.__dict__.update(kwargs)

    class Logger:
        def info(self, *args: Any, **kwargs: Any) -> None:
            pass

        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

        def error(self, *args: Any, **kwargs: Any) -> None:
            pass

        def exception(self, *args: Any, **kwargs: Any) -> None:
            pass

    class BaseModel:
        def __init__(self, **kwargs: Any) -> None:
            for key, value in kwargs.items():
                setattr(self, key, value)

    class Span:
        pass

    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")
    if not hasattr(pydantic_mod, "BaseModel"):
        pydantic_mod.BaseModel = BaseModel
    if not hasattr(pydantic_mod, "ConfigDict"):
        pydantic_mod.ConfigDict = lambda *args, **kwargs: dict(*args, **kwargs)
    if not hasattr(pydantic_mod, "Field"):
        pydantic_mod.Field = lambda default=..., **kwargs: default
    sys.modules["pydantic"] = pydantic_mod

    opentelemetry_mod = sys.modules.get("opentelemetry") or types.ModuleType("opentelemetry")
    trace_mod = sys.modules.get("opentelemetry.trace") or types.ModuleType("opentelemetry.trace")
    trace_mod.Span = Span
    opentelemetry_mod.trace = trace_mod
    sys.modules["opentelemetry"] = opentelemetry_mod
    sys.modules["opentelemetry.trace"] = trace_mod

    rtc_mod.EventEmitter = EventEmitter
    rtc_mod.AudioFrame = AudioFrame
    livekit_mod.rtc = rtc_mod
    metrics_base_mod.Metadata = Metadata
    metrics_mod.TTSMetrics = TTSMetrics
    log_mod.logger = Logger()
    telemetry_mod.trace_types = trace_types_mod
    telemetry_mod.tracer = tracer_mod
    telemetry_mod.utils = telemetry_utils_mod
    utils_mod.aio = aio_mod
    utils_mod.audio = audio_mod
    utils_mod.codecs = codecs_mod
    utils_mod.log_exceptions = lambda *args, **kwargs: (lambda fn: fn)
    utils_mod.shortuuid = lambda prefix="": prefix + "reference-id"

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.rtc"] = rtc_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.tts"] = tts_pkg
    sys.modules["livekit.agents.metrics"] = metrics_mod
    sys.modules["livekit.agents.metrics.base"] = metrics_base_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["livekit.agents.telemetry"] = telemetry_mod
    sys.modules["livekit.agents.telemetry.trace_types"] = trace_types_mod
    sys.modules["livekit.agents.telemetry.tracer"] = tracer_mod
    sys.modules["livekit.agents.telemetry.utils"] = telemetry_utils_mod
    sys.modules["livekit.agents.utils"] = utils_mod
    sys.modules["livekit.agents.utils.aio"] = aio_mod
    sys.modules["livekit.agents.utils.audio"] = audio_mod
    sys.modules["livekit.agents.utils.codecs"] = codecs_mod

    load_reference_types()
    load_reference_exceptions()

    spec = importlib.util.spec_from_file_location("livekit.agents.tts.tts", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference tts.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.tts.tts"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_tts_fallback():
    tts_module = load_reference_tts()
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/tts/fallback_adapter.py"

    stream_adapter_mod = types.ModuleType("livekit.agents.tts.stream_adapter")

    class StreamAdapter:
        pass

    stream_adapter_mod.StreamAdapter = StreamAdapter
    sys.modules["livekit.agents.tts.stream_adapter"] = stream_adapter_mod

    utils_mod = sys.modules["livekit.agents.utils"]
    aio_mod = sys.modules["livekit.agents.utils.aio"]
    if not hasattr(aio_mod, "cancel_and_wait"):
        async def cancel_and_wait(task: Any) -> None:
            task.cancel()
            try:
                await task
            except Exception:
                pass

        aio_mod.cancel_and_wait = cancel_and_wait
    utils_mod.aio = aio_mod

    tts_pkg = sys.modules["livekit.agents.tts"]
    for name in (
        "AudioEmitter",
        "ChunkedStream",
        "SynthesizedAudio",
        "SynthesizeStream",
        "TTS",
        "TTSCapabilities",
    ):
        setattr(tts_pkg, name, getattr(tts_module, name))

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.tts.fallback_adapter", path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference fallback_adapter.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.tts.fallback_adapter"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_tts_stream_adapter():
    tts_module = load_reference_tts()
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/tts/stream_adapter.py"

    tts_pkg = sys.modules["livekit.agents.tts"]
    for name in (
        "AudioEmitter",
        "ChunkedStream",
        "SynthesizedAudio",
        "SynthesizeStream",
        "TTS",
        "TTSCapabilities",
    ):
        setattr(tts_pkg, name, getattr(tts_module, name))

    tokenize_mod = sys.modules.get("livekit.agents.tokenize") or types.ModuleType(
        "livekit.agents.tokenize"
    )
    blingfire_mod = types.ModuleType("livekit.agents.tokenize.blingfire")

    class SentenceTokenizer:
        def __init__(self, *args: Any, **kwargs: Any) -> None:
            pass

    blingfire_mod.SentenceTokenizer = SentenceTokenizer
    tokenize_mod.SentenceTokenizer = SentenceTokenizer
    tokenize_mod.blingfire = blingfire_mod
    sys.modules["livekit.agents.tokenize"] = tokenize_mod
    sys.modules["livekit.agents.tokenize.blingfire"] = blingfire_mod

    stream_pacer_mod = types.ModuleType("livekit.agents.tts.stream_pacer")

    class SentenceStreamPacer:
        pass

    stream_pacer_mod.SentenceStreamPacer = SentenceStreamPacer
    sys.modules["livekit.agents.tts.stream_pacer"] = stream_pacer_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.tts.stream_adapter", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference tts stream_adapter.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.tts.stream_adapter"] = module
    spec.loader.exec_module(module)
    return module


def _repair_json_like(value: str) -> Any:
    out = value
    for pattern in (
        re.compile(r"<\|[^<>|]{0,40}\|>"),
        re.compile(r"<\|[^<>a-zA-Z0-9_]{0,10}"),
        re.compile(r"[^<>a-zA-Z0-9_]{0,10}\|>"),
        re.compile(r"<(?:start|end)_of_turn>"),
    ):
        out = pattern.sub("", out)
    out = re.sub(r"'([^'\\]*(?:\\.[^'\\]*)*)'", r'"\1"', out)
    out = re.sub(r'([{\[,]\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*:)', r'\1"\2"\3', out)
    out = re.sub(
        r'(:\s*)([A-Za-z_][A-Za-z0-9_ -]*)(\s*[,}\]])',
        lambda match: match.group(0)
        if match.group(2) in {"true", "false", "null"}
        else f'{match.group(1)}"{match.group(2).strip()}"{match.group(3)}',
        out,
    )
    out = re.sub(r",\s*([}\]])", r"\1", out).strip()

    stack: list[str] = []
    in_string = False
    escaped = False
    for ch in out:
        if in_string:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            continue
        if ch == '"':
            in_string = True
        elif ch == "{":
            stack.append("}")
        elif ch == "[":
            stack.append("]")
        elif ch in "}]":
            if not stack or stack[-1] != ch:
                stack = []
                break
            stack.pop()
    if not in_string and stack:
        out += "".join(reversed(stack))

    try:
        return json.loads(out)
    except Exception:
        return ""


def load_reference_llm_utils():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/llm/utils.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    rtc_mod = sys.modules.get("livekit.rtc") or types.ModuleType("livekit.rtc")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    llm_pkg = sys.modules.get("livekit.agents.llm") or types.ModuleType("livekit.agents.llm")
    llm_pkg.__path__ = [str(path.parent)]
    strict_mod = types.ModuleType("livekit.agents.llm._strict")
    chat_context_mod = types.ModuleType("livekit.agents.llm.chat_context")
    tool_context_mod = types.ModuleType("livekit.agents.llm.tool_context")
    log_mod = sys.modules.get("livekit.agents.log") or types.ModuleType("livekit.agents.log")
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    images_mod = types.ModuleType("livekit.agents.utils.images")
    json_repair_mod = types.ModuleType("json_repair")
    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")
    pydantic_fields_mod = types.ModuleType("pydantic.fields")
    pydantic_core_mod = sys.modules.get("pydantic_core") or types.ModuleType("pydantic_core")

    class VideoFrame:
        pass

    class Logger:
        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

        def error(self, *args: Any, **kwargs: Any) -> None:
            pass

        def exception(self, *args: Any, **kwargs: Any) -> None:
            pass

    class BaseModel:
        pass

    class FieldInfo:
        default = None
        description = None

    class FunctionTool:
        pass

    class RawFunctionTool:
        pass

    class ToolError(Exception):
        def __init__(self, message: str) -> None:
            super().__init__(message)
            self.message = message

    class StopResponse(Exception):
        pass

    class ImageContent:
        def __init__(self, image: Any = None, **kwargs: Any) -> None:
            self.image = image
            self.mime_type = kwargs.get("mime_type")
            self.inference_detail = kwargs.get("inference_detail", "auto")
            self.inference_width = kwargs.get("inference_width")
            self.inference_height = kwargs.get("inference_height")
            self._cache: dict[str, Any] = {}

    class FunctionCall:
        def __init__(
            self,
            *,
            id: str = "",
            call_id: str = "",
            name: str = "",
            arguments: str = "",
            **kwargs: Any,
        ) -> None:
            self.id = id
            self.call_id = call_id
            self.name = name
            self.arguments = arguments
            self.extra = kwargs.get("extra")
            self.group_id = kwargs.get("group_id")
            self.created_at = kwargs.get("created_at", 0.0)

    class FunctionCallOutput:
        def __init__(
            self,
            *,
            id: str = "",
            call_id: str = "",
            name: str = "",
            output: str = "",
            is_error: bool = False,
            **kwargs: Any,
        ) -> None:
            self.id = id
            self.call_id = call_id
            self.name = name
            self.output = output
            self.is_error = is_error
            self.created_at = kwargs.get("created_at", 0.0)

    rtc_mod.VideoFrame = VideoFrame
    livekit_mod.rtc = rtc_mod
    log_mod.logger = Logger()
    images_mod.EncodeOptions = object
    images_mod.ResizeOptions = object
    images_mod.encode = lambda *args, **kwargs: b""
    utils_mod.images = images_mod
    strict_mod.to_strict_json_schema = lambda schema: schema
    chat_context_mod.ChatContext = object
    chat_context_mod.ImageContent = ImageContent
    chat_context_mod.FunctionCall = FunctionCall
    chat_context_mod.FunctionCallOutput = FunctionCallOutput
    tool_context_mod.FunctionTool = FunctionTool
    tool_context_mod.RawFunctionTool = RawFunctionTool
    tool_context_mod.ToolError = ToolError
    tool_context_mod.StopResponse = StopResponse
    json_repair_mod.loads = _repair_json_like

    if not hasattr(pydantic_mod, "BaseModel"):
        pydantic_mod.BaseModel = BaseModel
    if not hasattr(pydantic_mod, "TypeAdapter"):
        pydantic_mod.TypeAdapter = object
    if not hasattr(pydantic_mod, "create_model"):
        pydantic_mod.create_model = lambda *args, **kwargs: BaseModel
    if not hasattr(pydantic_mod, "Field"):
        pydantic_mod.Field = lambda default=..., **kwargs: default
    pydantic_fields_mod.Field = pydantic_mod.Field
    pydantic_fields_mod.FieldInfo = FieldInfo
    pydantic_core_mod.PydanticUndefined = object()
    pydantic_core_mod.from_json = json.loads

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.rtc"] = rtc_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.llm"] = llm_pkg
    sys.modules["livekit.agents.llm._strict"] = strict_mod
    sys.modules["livekit.agents.llm.chat_context"] = chat_context_mod
    sys.modules["livekit.agents.llm.tool_context"] = tool_context_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["livekit.agents.utils"] = utils_mod
    sys.modules["livekit.agents.utils.images"] = images_mod
    sys.modules["json_repair"] = json_repair_mod
    sys.modules["pydantic"] = pydantic_mod
    sys.modules["pydantic.fields"] = pydantic_fields_mod
    sys.modules["pydantic_core"] = pydantic_core_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.llm.utils", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference llm utils.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.llm.utils"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_tool_context():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/llm/tool_context.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    llm_pkg = sys.modules.get("livekit.agents.llm") or types.ModuleType("livekit.agents.llm")
    llm_pkg.__path__ = [str(path.parent)]
    provider_format_mod = types.ModuleType("livekit.agents.llm._provider_format")
    log_mod = sys.modules.get("livekit.agents.log") or types.ModuleType("livekit.agents.log")
    pydantic_mod = sys.modules.get("pydantic") or types.ModuleType("pydantic")

    class Logger:
        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

        def error(self, *args: Any, **kwargs: Any) -> None:
            pass

        def exception(self, *args: Any, **kwargs: Any) -> None:
            pass

    if not hasattr(pydantic_mod, "Field"):
        pydantic_mod.Field = lambda default=..., **kwargs: default

    provider_format_mod.build_oai_function_description = lambda *args, **kwargs: {}
    provider_format_mod.build_raw_oai_function_description = lambda *args, **kwargs: {}
    provider_format_mod.build_aws_function_description = lambda *args, **kwargs: {}
    log_mod.logger = Logger()

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.llm"] = llm_pkg
    sys.modules["livekit.agents.llm._provider_format"] = provider_format_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["pydantic"] = pydantic_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.llm.tool_context", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference tool_context.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.llm.tool_context"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_audio():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/utils/audio.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    rtc_mod = sys.modules.get("livekit.rtc") or types.ModuleType("livekit.rtc")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    utils_pkg = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    aio_pkg = sys.modules.get("livekit.agents.utils.aio") or types.ModuleType(
        "livekit.agents.utils.aio"
    )
    aio_utils_mod = types.ModuleType("livekit.agents.utils.aio.utils")
    codecs_mod = types.ModuleType("livekit.agents.utils.codecs")
    log_mod = sys.modules.get("livekit.agents.log") or types.ModuleType("livekit.agents.log")
    aiofiles_mod = types.ModuleType("aiofiles")
    numpy_mod = types.ModuleType("numpy")
    numpy_typing_mod = types.ModuleType("numpy.typing")

    class AudioFrame:
        def __init__(
            self,
            *,
            data: bytes | bytearray,
            sample_rate: int,
            num_channels: int,
            samples_per_channel: int,
        ) -> None:
            self.data = bytes(data)
            self.sample_rate = sample_rate
            self.num_channels = num_channels
            self.samples_per_channel = samples_per_channel

        @property
        def duration(self) -> float:
            if self.sample_rate == 0:
                return 0.0
            return self.samples_per_channel / self.sample_rate

    class Logger:
        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

    def combine_audio_frames(frames: Any) -> AudioFrame:
        if isinstance(frames, AudioFrame):
            return frames
        frame_list = list(frames)
        if not frame_list:
            return AudioFrame(data=b"", sample_rate=0, num_channels=0, samples_per_channel=0)
        first = frame_list[0]
        return AudioFrame(
            data=b"".join(frame.data for frame in frame_list),
            sample_rate=first.sample_rate,
            num_channels=first.num_channels,
            samples_per_channel=sum(frame.samples_per_channel for frame in frame_list),
        )

    rtc_mod.AudioFrame = AudioFrame
    rtc_mod.combine_audio_frames = combine_audio_frames
    rtc_mod.AudioResampler = object
    rtc_mod.AudioResamplerQuality = types.SimpleNamespace(QUICK="quick")
    livekit_mod.rtc = rtc_mod
    log_mod.logger = Logger()
    aio_utils_mod.cancel_and_wait = lambda task: task
    numpy_mod.int16 = "int16"
    numpy_mod.zeros = lambda *args, **kwargs: []
    numpy_mod.frombuffer = lambda *args, **kwargs: []
    numpy_mod.sum = lambda *args, **kwargs: []
    numpy_typing_mod.DTypeLike = object

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.rtc"] = rtc_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.utils"] = utils_pkg
    sys.modules["livekit.agents.utils.aio"] = aio_pkg
    sys.modules["livekit.agents.utils.aio.utils"] = aio_utils_mod
    sys.modules["livekit.agents.utils.codecs"] = codecs_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["aiofiles"] = aiofiles_mod
    sys.modules["numpy"] = numpy_mod
    sys.modules["numpy.typing"] = numpy_typing_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.utils.audio", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference audio.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.audio"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_image():
    image_path = repo_root() / "refs/agents/livekit-agents/livekit/agents/utils/images/image.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    rtc_mod = types.ModuleType("livekit.rtc")

    class VideoBufferType:
        RGBA = "rgba"

    rtc_mod.VideoBufferType = VideoBufferType
    rtc_mod.VideoFrame = object
    livekit_mod.rtc = rtc_mod
    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.rtc"] = rtc_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.utils.images.image", image_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference image.py from {image_path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.images.image"] = module
    spec.loader.exec_module(module)
    return module


class _VideoFrame:
    def __init__(self, data: bytes, width: int, height: int, frame_type: str = "rgba") -> None:
        self.data = data
        self.width = width
        self.height = height
        self.type = frame_type


def load_reference_token_stream():
    token_stream_path = repo_root() / "refs/agents/livekit-agents/livekit/agents/tokenize/token_stream.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    tokenize_mod = sys.modules.get("livekit.agents.tokenize") or types.ModuleType(
        "livekit.agents.tokenize"
    )
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    aio_mod = types.ModuleType("livekit.agents.utils.aio")
    tokenizer_mod = types.ModuleType("livekit.agents.tokenize.tokenizer")

    class Chan:
        def __init__(self) -> None:
            self._items: list[Any] = []
            self.closed = False

        def __class_getitem__(cls, item: Any) -> type:
            return cls

        def send_nowait(self, item: Any) -> None:
            if self.closed:
                raise RuntimeError("channel is closed")
            self._items.append(item)

        def close(self) -> None:
            self.closed = True

        async def __anext__(self) -> Any:
            if self._items:
                return self._items.pop(0)
            if self.closed:
                raise StopAsyncIteration
            raise RuntimeError("channel has no queued item")

    class TokenData:
        def __init__(self, *, token: str, segment_id: str) -> None:
            self.token = token
            self.segment_id = segment_id

    class SentenceStream:
        pass

    class WordStream:
        pass

    aio_mod.Chan = Chan
    tokenizer_mod.TokenData = TokenData
    tokenizer_mod.SentenceStream = SentenceStream
    tokenizer_mod.WordStream = WordStream
    utils_mod.aio = aio_mod
    utils_mod.shortuuid = lambda: "segment-id"

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.tokenize"] = tokenize_mod
    sys.modules["livekit.agents.utils"] = utils_mod
    sys.modules["livekit.agents.utils.aio"] = aio_mod
    sys.modules["livekit.agents.tokenize.tokenizer"] = tokenizer_mod

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.tokenize.token_stream", token_stream_path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference token_stream.py from {token_stream_path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.tokenize.token_stream"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_connection_pool():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/utils/connection_pool.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )
    log_mod = types.ModuleType("livekit.agents.log")
    aio_mod = types.ModuleType("livekit.agents.utils.aio")

    class Logger:
        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

    async def gracefully_cancel(task: Any) -> None:
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass

    log_mod.logger = Logger()
    aio_mod.gracefully_cancel = gracefully_cancel
    utils_mod.aio = aio_mod

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["livekit.agents.utils"] = utils_mod
    sys.modules["livekit.agents.utils.aio"] = aio_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.utils.connection_pool", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference connection_pool.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.connection_pool"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_dtmf_utils():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/beta/workflows/utils.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    beta_mod = sys.modules.get("livekit.agents.beta") or types.ModuleType("livekit.agents.beta")
    workflows_mod = sys.modules.get("livekit.agents.beta.workflows") or types.ModuleType(
        "livekit.agents.beta.workflows"
    )
    llm_mod = sys.modules.get("livekit.agents.llm") or types.ModuleType("livekit.agents.llm")
    chat_context_mod = types.ModuleType("livekit.agents.llm.chat_context")
    types_mod = sys.modules.get("livekit.agents.types") or types.ModuleType("livekit.agents.types")

    class Instructions(str):
        pass

    class NotGivenOr:
        def __class_getitem__(cls, item: Any) -> object:
            return object

    chat_context_mod.Instructions = Instructions
    types_mod.NOT_GIVEN = object()
    types_mod.NotGivenOr = NotGivenOr

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.beta"] = beta_mod
    sys.modules["livekit.agents.beta.workflows"] = workflows_mod
    sys.modules["livekit.agents.llm"] = llm_mod
    sys.modules["livekit.agents.llm.chat_context"] = chat_context_mod
    sys.modules["livekit.agents.types"] = types_mod

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.beta.workflows.utils", path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference workflow utils.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.beta.workflows.utils"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_end_call_tool():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/beta/tools/end_call.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    beta_mod = sys.modules.get("livekit.agents.beta") or types.ModuleType("livekit.agents.beta")
    tools_mod = sys.modules.get("livekit.agents.beta.tools") or types.ModuleType(
        "livekit.agents.beta.tools"
    )
    job_mod = types.ModuleType("livekit.agents.job")
    llm_mod = types.ModuleType("livekit.agents.llm")
    log_mod = types.ModuleType("livekit.agents.log")
    voice_events_mod = types.ModuleType("livekit.agents.voice.events")
    speech_handle_mod = types.ModuleType("livekit.agents.voice.speech_handle")

    class RealtimeModel:
        pass

    class Toolset:
        class ToolCalledEvent:
            def __init__(self, **kwargs: Any) -> None:
                self.__dict__.update(kwargs)

        class ToolCompletedEvent:
            def __init__(self, **kwargs: Any) -> None:
                self.__dict__.update(kwargs)

        def __init__(self, **kwargs: Any) -> None:
            self.__dict__.update(kwargs)

    def function_tool(func: Any = None, **kwargs: Any) -> Any:
        if func is None:
            return lambda wrapped: wrapped
        func.__tool_kwargs__ = kwargs
        return func

    class Logger:
        def debug(self, *args: Any, **kwargs: Any) -> None:
            pass

        def warning(self, *args: Any, **kwargs: Any) -> None:
            pass

        def info(self, *args: Any, **kwargs: Any) -> None:
            pass

    class CloseEvent:
        pass

    class RunContext:
        pass

    class SpeechCreatedEvent:
        pass

    class SpeechHandle:
        pass

    job_mod.get_job_context = lambda: None
    llm_mod.RealtimeModel = RealtimeModel
    llm_mod.Toolset = Toolset
    llm_mod.function_tool = function_tool
    log_mod.logger = Logger()
    voice_events_mod.CloseEvent = CloseEvent
    voice_events_mod.RunContext = RunContext
    voice_events_mod.SpeechCreatedEvent = SpeechCreatedEvent
    speech_handle_mod.SpeechHandle = SpeechHandle

    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.beta"] = beta_mod
    sys.modules["livekit.agents.beta.tools"] = tools_mod
    sys.modules["livekit.agents.job"] = job_mod
    sys.modules["livekit.agents.llm"] = llm_mod
    sys.modules["livekit.agents.log"] = log_mod
    sys.modules["livekit.agents.voice.events"] = voice_events_mod
    sys.modules["livekit.agents.voice.speech_handle"] = speech_handle_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.beta.tools.end_call", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference end_call.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.beta.tools.end_call"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_plugin():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/plugin.py"

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType(
        "livekit.agents.utils"
    )

    class EventEmitter:
        def __init__(self) -> None:
            self.events: list[tuple[str, Any]] = []

        def __class_getitem__(cls, item: Any) -> type:
            return cls

        def emit(self, event: str, payload: Any) -> None:
            self.events.append((event, payload))

    utils_mod.EventEmitter = EventEmitter
    sys.modules["livekit"] = livekit_mod
    sys.modules["livekit.agents"] = agents_mod
    sys.modules["livekit.agents.utils"] = utils_mod

    spec = importlib.util.spec_from_file_location("livekit.agents.plugin", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference plugin.py from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.plugin"] = module
    spec.loader.exec_module(module)
    return module


async def _token_stream_next(stream: Any) -> dict[str, Any]:
    try:
        token = await stream.__anext__()
    except StopAsyncIteration:
        return {"name": "next", "error": True, "error_class": "eof"}
    except Exception:
        return {"name": "next", "error": True, "error_class": "error"}
    return {"name": "next", "error": False, "error_class": "", "token": token.token}
