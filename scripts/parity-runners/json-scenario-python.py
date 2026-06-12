#!/usr/bin/env python3
import importlib
import json
import sys
from pathlib import Path
from typing import Any, Callable


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def load_scenario(path: str) -> dict[str, Any]:
    with open(path, "r", encoding="utf-8") as file:
        scenario = json.load(file)
    if not isinstance(scenario, dict):
        raise ValueError("scenario JSON must be an object")
    validate_scenario(scenario)
    return scenario


def validate_scenario(scenario: dict[str, Any]) -> None:
    name = scenario.get("name")
    if not isinstance(name, str) or not name:
        raise ValueError("scenario name is required")
    if scenario.get("case_type") != "cross-runtime":
        raise ValueError(f"[{name}] case_type must be cross-runtime")
    if "input" not in scenario:
        raise ValueError(f"[{name}] input is required")
    if not isinstance(scenario.get("python_entrypoint"), str) or not scenario["python_entrypoint"]:
        raise ValueError(f"[{name}] python_entrypoint is required")
    if not isinstance(scenario.get("go_handler"), str) or not scenario["go_handler"]:
        raise ValueError(f"[{name}] go_handler is required")
    if scenario.get("compare_mode") != "json_equal":
        raise ValueError(f"[{name}] compare_mode must be json_equal")


def load_entrypoint(entrypoint: str) -> Callable[[Any], Any]:
    module_name, separator, function_name = entrypoint.partition(":")
    if not separator or not module_name or not function_name:
        raise ValueError(f"python_entrypoint must be module:function, got {entrypoint!r}")
    root = str(repo_root())
    if root not in sys.path:
        sys.path.insert(0, root)
    module = importlib.import_module(module_name)
    function = getattr(module, function_name)
    if not callable(function):
        raise TypeError(f"{entrypoint} is not callable")
    return function


def run_scenario(path: str) -> dict[str, Any]:
    scenario = load_scenario(path)
    entrypoint = load_entrypoint(scenario["python_entrypoint"])
    expected_error = scenario.get("expected_error_substring") or ""
    try:
        result = entrypoint(scenario["input"])
    except Exception as exc:
        if not expected_error:
            raise
        message = str(exc)
        if expected_error not in message:
            raise RuntimeError(
                f"[{scenario['name']}] Python error {message!r} does not contain "
                f"expected substring {expected_error!r}"
            ) from exc
        return {"error": message}
    if expected_error:
        raise RuntimeError(
            f"[{scenario['name']}] expected Python error containing {expected_error!r}"
        )
    return result


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: json-scenario-python.py SCENARIO_JSON", file=sys.stderr)
        return 2
    try:
        output = run_scenario(sys.argv[1])
    except Exception as exc:
        print(exc, file=sys.stderr)
        return 2
    json.dump(output, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
