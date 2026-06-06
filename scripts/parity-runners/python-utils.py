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


def restore_env(name: str, original: str | None, original_present: bool) -> None:
    if original_present:
        os.environ[name] = original or ""
    else:
        os.environ.pop(name, None)


def string_values(input_data: dict, field: str, default: list[str]) -> list[str]:
    values = input_data.get(field, default)
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError(f"{field} must be a list of strings")
    return values


def optional_string_values(input_data: dict, field: str, default: list[str | None]) -> list[str | None]:
    values = input_data.get(field, default)
    if not isinstance(values, list) or not all(
        value is None or isinstance(value, str) for value in values
    ):
        raise ValueError(f"{field} must be a list of strings or null")
    return values


def run_dev_mode_env_exact(input_data: dict) -> dict:
    misc = load_reference_misc()
    values = string_values(input_data, "env_values", ["1", "", "true", "on"])

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
        restore_env("LIVEKIT_DEV_MODE", original, original_present)

    return {"contract": "dev-mode-env-exact", "events": events}


def run_hosted_env_presence(input_data: dict) -> dict:
    misc = load_reference_misc()
    values = optional_string_values(
        input_data,
        "env_values",
        [None, "", "https://hosted.example"],
    )

    original = os.environ.get("LIVEKIT_REMOTE_EOT_URL")
    original_present = "LIVEKIT_REMOTE_EOT_URL" in os.environ
    events = []
    try:
        for value in values:
            if value is None:
                os.environ.pop("LIVEKIT_REMOTE_EOT_URL", None)
            else:
                os.environ["LIVEKIT_REMOTE_EOT_URL"] = value
            events.append(
                {
                    "name": "is_hosted",
                    "env": value,
                    "result": bool(misc.is_hosted()),
                }
            )
    finally:
        restore_env("LIVEKIT_REMOTE_EOT_URL", original, original_present)

    return {"contract": "hosted-env-presence", "events": events}


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: python-utils.py INPUT_JSON", file=sys.stderr)
        return 2

    input_data = load_input(sys.argv[1])
    contract = input_data.get("contract", "dev-mode-env-exact")
    if contract == "dev-mode-env-exact":
        output = run_dev_mode_env_exact(input_data)
    elif contract == "hosted-env-presence":
        output = run_hosted_env_presence(input_data)
    else:
        print(f"unsupported contract: {contract}", file=sys.stderr)
        return 2

    json.dump(output, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
