from common import *  # noqa: F403

def dtmf_event_code(input_data: Any) -> dict[str, Any]:
    values = input_data.get("events", ["a", "12"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("events must be a list of strings")
    module = load_reference_dtmf_utils()
    events = []
    for value in values:
        code = 0
        error = False
        try:
            code = module.dtmf_event_to_code(module.DtmfEvent(value))
        except Exception:
            error = True
        events.append(
            {
                "name": "dtmf_event_to_code",
                "input": value,
                "code": code,
                "error": error,
                "error_class": "error" if error else "",
            }
        )
    return {"contract": "dtmf-event-code", "events": events}


def dtmf_tool(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "parameters")
    if action == "parameters":
        module = load_reference_dtmf_utils()
        enum_values = [event.value for event in module.DtmfEvent]
        return {
            "contract": "send-dtmf-tool",
            "events": [
                {
                    "name": "parameters",
                    "parameters": {
                        "type": "object",
                        "additionalProperties": False,
                        "properties": {
                            "events": {
                                "type": "array",
                                "items": {"type": "string", "enum": enum_values},
                            }
                        },
                        "required": ["events"],
                    },
                }
            ],
        }
    if action == "execute":
        values = input_data.get("events", ["X"])
        if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
            raise ValueError("events must be a list of strings")
        module = load_reference_dtmf_utils()
        output = ""
        error = False
        try:
            for value in values:
                code = module.dtmf_event_to_code(module.DtmfEvent(value))
                _ = code
            output = f"Successfully sent DTMF events: {', '.join(values)}"
        except Exception as exc:
            output = f"Failed to send DTMF event: {value}. Error: {str(exc)}"
        return {
            "contract": "send-dtmf-tool",
            "events": [
                {
                    "name": "execute",
                    "invalid_event": values[0] if values else "",
                    "output_contains_failed": "Failed to send DTMF event:" in output,
                    "error": error,
                    "error_class": "error" if error else "",
                }
            ],
        }
    raise ValueError(f"unsupported dtmf tool action {action!r}")


def end_call_tool(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "description")
    if action == "description":
        module = load_reference_end_call_tool()
        description = module.END_CALL_DESCRIPTION
        return {
            "contract": "end-call-tool",
            "events": [
                {
                    "name": "description",
                    "contains_user_done_guidance": "The user clearly indicates they are done"
                    in description,
                    "contains_agent_completion_trigger": "agent determines the conversation is complete"
                    in description,
                    "contains_no_pause_hold_transfer_rule": "pause, hold, or transfer" in description,
                }
            ],
        }
    if action == "parameters":
        _ = load_reference_end_call_tool()
        return {
            "contract": "end-call-tool",
            "events": [
                {
                    "name": "parameters",
                    "parameters": {
                        "type": "object",
                        "additionalProperties": False,
                        "properties": {},
                        "required": [],
                    },
                }
            ],
        }
    raise ValueError(f"unsupported end call tool action {action!r}")


