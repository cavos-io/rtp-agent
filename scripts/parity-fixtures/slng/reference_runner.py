#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import sys
import types
from pathlib import Path
from typing import Any


def reference_dir() -> Path:
    root = Path(__file__).resolve().parents[3]
    relative = Path(
        "refs/agents/livekit-plugins/livekit-plugins-slng/livekit/plugins/slng"
    )
    direct = root / relative
    if direct.is_dir():
        return direct

    dot_git = root / ".git"
    if dot_git.is_file():
        value = dot_git.read_text(encoding="utf-8").strip()
        if value.startswith("gitdir:"):
            git_dir = Path(value.removeprefix("gitdir:").strip())
            if not git_dir.is_absolute():
                git_dir = (root / git_dir).resolve()
            for parent in (git_dir, *git_dir.parents):
                if parent.name == ".git":
                    linked = parent.parent / relative
                    if linked.is_dir():
                        return linked
    raise FileNotFoundError(f"vendored SLNG reference not found under {root}")


def load_reference_modules() -> tuple[Any, Any]:
    directory = reference_dir()
    package_name = "_rtp_agent_slng_reference"
    package = types.ModuleType(package_name)
    package.__path__ = [str(directory)]
    sys.modules[package_name] = package

    loaded = {}
    for name in ("gateway_adapter", "connection"):
        path = directory / f"{name}.py"
        spec = importlib.util.spec_from_file_location(f"{package_name}.{name}", path)
        if spec is None or spec.loader is None:
            raise RuntimeError(f"cannot load SLNG reference module from {path}")
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        loaded[name] = module
    return loaded["connection"], loaded["gateway_adapter"]


def read_scenario() -> dict[str, Any]:
    if len(sys.argv) > 2:
        raise ValueError("usage: reference_runner.py [SCENARIO_JSON]")
    if len(sys.argv) == 2:
        with open(sys.argv[1], "r", encoding="utf-8") as file:
            scenario = json.load(file)
    else:
        scenario = json.load(sys.stdin)
    if not isinstance(scenario, dict):
        raise ValueError("scenario JSON must be an object")
    return scenario


def lowercase_headers(headers: dict[str, str]) -> dict[str, str]:
    return {key.lower(): value for key, value in headers.items()}


def run_operation(
    operation: str,
    inputs: dict[str, Any],
    connection: Any,
    gateway: Any,
) -> Any:
    if operation == "bridge_endpoint":
        return connection.bridge_endpoint(**inputs)
    if operation == "validate_model_identifier":
        return gateway.validate_model_identifier(**inputs)
    if operation == "bridge_model":
        return connection.bridge_model(**inputs)
    if operation == "normalize_region_override":
        return gateway.normalize_region_override(**inputs)
    if operation == "normalize_world_part_override":
        return gateway.normalize_world_part_override(**inputs)
    if operation == "external_tracking_headers":
        return lowercase_headers(gateway.build_external_tracking_headers(**inputs))
    if operation == "extract_error_status":
        return gateway.extract_error_status(inputs["frame"])
    if operation == "build_stt_init_payload":
        return gateway.build_stt_init_payload(**inputs)
    if operation == "build_tts_init_payload":
        return gateway.build_tts_init_payload(**inputs)
    if operation == "candidate_primary_recovery":
        state = connection.CandidateState(
            inputs["count"],
            inputs["cooldown_seconds"],
        )
        times = iter(
            (
                inputs["failed_at_seconds"],
                inputs["before_seconds"],
                inputs["after_seconds"],
            )
        )
        original_monotonic = connection.time.monotonic
        connection.time.monotonic = lambda: next(times)
        try:
            return {
                "next_after_primary_failure": state.advance(0),
                "before_cooldown": state.start(),
                "after_cooldown": state.start(),
            }
        finally:
            connection.time.monotonic = original_monotonic
    raise ValueError(f"unknown operation {operation!r}")


def run_case(case: dict[str, Any], connection: Any, gateway: Any) -> dict[str, Any]:
    name = case.get("name")
    operation = case.get("operation")
    inputs = case.get("input")
    if not isinstance(name, str) or not name:
        raise ValueError("case name is required")
    if not isinstance(operation, str) or not operation:
        raise ValueError(f"[{name}] operation is required")
    if not isinstance(inputs, dict):
        raise ValueError(f"[{name}] input must be an object")

    result = None
    error = None
    try:
        result = run_operation(operation, inputs, connection, gateway)
    except Exception as exc:
        error = {"type": type(exc).__name__, "message": str(exc)}
    actual = {"result": result, "error": error}
    if actual != case.get("expected"):
        raise AssertionError(
            f"[{name}] actual {json.dumps(actual, sort_keys=True)} != "
            f"expected {json.dumps(case.get('expected'), sort_keys=True)}"
        )
    return {
        "case_type": "cross-runtime",
        "name": name,
        **actual,
    }


def main() -> int:
    try:
        scenario = read_scenario()
        name = scenario.get("name")
        if not isinstance(name, str) or not name:
            raise ValueError("scenario name is required")
        if scenario.get("case_type") != "cross-runtime":
            raise ValueError(f"[{name}] case_type must be cross-runtime")
        if scenario.get("compare_mode") != "json_equal":
            raise ValueError(f"[{name}] compare_mode must be json_equal")
        cases = scenario.get("input")
        if not isinstance(cases, list):
            raise ValueError(f"[{name}] input must be a list")

        connection, gateway = load_reference_modules()
        output = {
            "case_type": "cross-runtime",
            "name": name,
            "result": {
                "scenarios": [
                    run_case(case, connection, gateway) for case in cases
                ]
            },
            "error": None,
        }
        json.dump(output, sys.stdout, sort_keys=True, separators=(",", ":"))
        sys.stdout.write("\n")
        return 0
    except Exception as exc:
        print(f"{type(exc).__name__}: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
