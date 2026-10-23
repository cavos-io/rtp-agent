"""Execute the reference's pure attribute filter without importing voice providers."""
import ast
import runpy
import types
from collections.abc import Mapping
from pathlib import Path
from typing import Any


def trace_content(input_data: Any) -> dict[str, Any]:
    root = Path(__file__).resolve().parents[3]
    telemetry = root / "refs/agents/livekit-agents/livekit/agents/telemetry"
    trace_types = types.SimpleNamespace(**runpy.run_path(str(telemetry / "trace_types.py")))
    source = ast.parse((telemetry / "pii.py").read_text())
    # These are the actual source definitions, not a second implementation.
    names = {"GEN_AI_PII_ATTRIBUTES", "is_pii_attribute", "filter_attributes"}
    nodes = [node for node in source.body if
             isinstance(node, ast.FunctionDef) and node.name in names or
             isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name)
             and node.target.id in names]
    constants = ast.parse((telemetry / "utils.py").read_text())
    redacted = next(ast.literal_eval(node.value) for node in constants.body
                    if isinstance(node, ast.Assign) and any(
                        isinstance(target, ast.Name) and target.id == "REDACTED_EXCEPTION_MESSAGE"
                        for target in node.targets))
    namespace = {"trace_types": trace_types, "Mapping": Mapping, "Any": Any,
                 "REDACTED_EXCEPTION_MESSAGE": redacted}
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(telemetry / "pii.py"), "exec"), namespace)
    return {"attributes": namespace["filter_attributes"](input_data["attributes"])}
