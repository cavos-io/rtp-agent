#!/usr/bin/env python3
import importlib.util
import json
import os
import sys
import types
from pathlib import Path


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def load_reference_misc():
    root = repo_root()
    misc_path = root / "refs/agents/livekit-agents/livekit/agents/utils/misc.py"

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

    types_mod.NotGiven = NotGiven
    types_mod.NotGivenOr = object

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


def load_input(path: str) -> dict:
    with open(path, "r", encoding="utf-8") as file:
        data = json.load(file)
    if not isinstance(data, dict):
        raise ValueError("input JSON must be an object")
    return data


def run_dev_mode_env_exact(input_data: dict) -> dict:
    misc = load_reference_misc()
    values = input_data.get("env_values", ["1", "", "true", "on"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("env_values must be a list of strings")

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


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: python-utils.py INPUT_JSON", file=sys.stderr)
        return 2

    input_data = load_input(sys.argv[1])
    contract = input_data.get("contract", "dev-mode-env-exact")
    if contract != "dev-mode-env-exact":
        print(f"unsupported contract: {contract}", file=sys.stderr)
        return 2

    output = run_dev_mode_env_exact(input_data)
    json.dump(output, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
