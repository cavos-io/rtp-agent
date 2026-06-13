from dataclasses import asdict

from common import *  # noqa: F403


def load_reference_vad():
    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/vad.py"  # noqa: F405

    livekit_mod = sys.modules.get("livekit") or types.ModuleType("livekit")  # noqa: F405
    rtc_mod = sys.modules.get("livekit.rtc") or types.ModuleType("livekit.rtc")  # noqa: F405
    agents_mod = sys.modules.get("livekit.agents") or types.ModuleType("livekit.agents")  # noqa: F405
    metrics_mod = sys.modules.get("livekit.agents.metrics") or types.ModuleType("livekit.agents.metrics")  # noqa: F405
    metrics_base_mod = sys.modules.get("livekit.agents.metrics.base") or types.ModuleType("livekit.agents.metrics.base")  # noqa: F405
    utils_mod = sys.modules.get("livekit.agents.utils") or types.ModuleType("livekit.agents.utils")  # noqa: F405
    aio_mod = sys.modules.get("livekit.agents.utils.aio") or types.ModuleType("livekit.agents.utils.aio")  # noqa: F405

    class EventEmitter:
        def __class_getitem__(cls, _item):
            return cls

        def __init__(self) -> None:
            pass

        def emit(self, *_args: Any, **_kwargs: Any) -> None:  # noqa: F405
            pass

    class AudioFrame:
        pass

    class Metadata:
        def __init__(self, *, model_name: str, model_provider: str) -> None:
            self.model_name = model_name
            self.model_provider = model_provider

    class VADMetrics:
        def __init__(self, **kwargs: Any) -> None:  # noqa: F405
            self.__dict__.update(kwargs)

    class Chan:
        def __class_getitem__(cls, _item):
            return cls

    class Itertools:
        @staticmethod
        def tee(*_args: Any, **_kwargs: Any) -> tuple[()]:  # noqa: F405
            return ()

    async def cancel_and_wait(_task: Any) -> None:  # noqa: F405
        return None

    rtc_mod.EventEmitter = EventEmitter
    rtc_mod.AudioFrame = AudioFrame
    metrics_base_mod.Metadata = Metadata
    metrics_mod.VADMetrics = VADMetrics
    aio_mod.Chan = Chan
    aio_mod.itertools = Itertools()
    aio_mod.cancel_and_wait = cancel_and_wait
    utils_mod.aio = aio_mod

    livekit_mod.rtc = rtc_mod
    agents_mod.__path__ = [str(path.parent)]
    metrics_mod.base = metrics_base_mod
    sys.modules["livekit"] = livekit_mod  # noqa: F405
    sys.modules["livekit.rtc"] = rtc_mod  # noqa: F405
    sys.modules["livekit.agents"] = agents_mod  # noqa: F405
    sys.modules["livekit.agents.metrics"] = metrics_mod  # noqa: F405
    sys.modules["livekit.agents.metrics.base"] = metrics_base_mod  # noqa: F405
    sys.modules["livekit.agents.utils"] = utils_mod  # noqa: F405
    sys.modules["livekit.agents.utils.aio"] = aio_mod  # noqa: F405

    spec = importlib.util.spec_from_file_location("livekit.agents.vad", path)  # noqa: F405
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference vad.py from {path}")
    module = importlib.util.module_from_spec(spec)  # noqa: F405
    sys.modules["livekit.agents.vad"] = module  # noqa: F405
    spec.loader.exec_module(module)
    return module


def vad_value_objects(input_data: Any) -> dict[str, Any]:  # noqa: F405
    action = input_data.get("action", "capabilities_json")
    module = load_reference_vad()
    if action == "capabilities_json":
        capabilities = module.VADCapabilities(
            update_interval=input_data.get("update_interval", 0.5)
        )
        payload = asdict(capabilities)
        return {
            "contract": "vad-capabilities-json",
            "events": [
                {
                    "name": "capabilities_json",
                    "update_interval": payload["update_interval"],
                    "has_go_field_names": "UpdateInterval" in payload,
                }
            ],
        }
    if action == "event_json":
        event = module.VADEvent(
            type=module.VADEventType.INFERENCE_DONE,
            samples_index=320,
            timestamp=1.25,
            speech_duration=0.5,
            silence_duration=0.75,
            probability=0.875,
            inference_duration=0.01,
            speaking=True,
            raw_accumulated_silence=0.125,
            raw_accumulated_speech=0.25,
        )
        payload = asdict(event)
        return {
            "contract": "vad-event-json",
            "events": [
                {
                    "name": "event_json",
                    "type": payload["type"].value,
                    "samples_index": payload["samples_index"],
                    "timestamp": payload["timestamp"],
                    "speech_duration": payload["speech_duration"],
                    "silence_duration": payload["silence_duration"],
                    "frames_length": len(payload["frames"]),
                    "probability": payload["probability"],
                    "inference_duration": payload["inference_duration"],
                    "speaking": payload["speaking"],
                    "raw_accumulated_silence": payload["raw_accumulated_silence"],
                    "raw_accumulated_speech": payload["raw_accumulated_speech"],
                    "has_go_field_names": any(
                        name in payload
                        for name in [
                            "SamplesIndex",
                            "SpeechDuration",
                            "InferenceDuration",
                        ]
                    ),
                }
            ],
        }
    raise ValueError(f"unsupported vad value-object action {action!r}")
