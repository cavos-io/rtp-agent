import importlib.util
import os
import sys
import types
from pathlib import Path
from typing import Any


def repo_root() -> Path:
    return Path(__file__).resolve().parents[1]


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


def dev_mode_env_exact(input_data: Any) -> dict[str, Any]:
    if not isinstance(input_data, dict):
        raise ValueError("input must be an object")
    values = input_data.get("env_values", ["1", "", "true", "on"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("env_values must be a list of strings")

    misc = load_reference_misc()
    original = os.environ.get("LIVEKIT_DEV_MODE")
    original_present = "LIVEKIT_DEV_MODE" in os.environ
    events = []
    try:
        for value in values:
            os.environ["LIVEKIT_DEV_MODE"] = value
            events.append(
                {
                    "name": "is_dev_mode",
                    "env": value,
                    "result": bool(misc.is_dev_mode()),
                }
            )
    finally:
        if original_present:
            os.environ["LIVEKIT_DEV_MODE"] = original or ""
        else:
            os.environ.pop("LIVEKIT_DEV_MODE", None)

    return {"contract": "dev-mode-env-exact", "events": events}
