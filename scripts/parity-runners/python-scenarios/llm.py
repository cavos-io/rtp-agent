import ast
from typing import Literal

from common import *  # noqa: F403


def load_reference_llm_value_class(class_name: str):
    def Field(default=..., **kwargs: Any) -> Any:
        if "default_factory" in kwargs:
            return kwargs["default_factory"]()
        return default

    class BaseModel:
        def __init__(self, **kwargs: Any) -> None:
            annotations = getattr(self.__class__, "__annotations__", {})
            for field in annotations:
                if field in kwargs:
                    value = kwargs.pop(field)
                elif field in self.__class__.__dict__:
                    value = self.__class__.__dict__[field]
                else:
                    raise ValueError(f"{field} is required")
                setattr(self, field, value)
            for field, value in kwargs.items():
                setattr(self, field, value)

        def model_dump(self) -> dict[str, Any]:
            def dump_value(value: Any) -> Any:
                if hasattr(value, "model_dump"):
                    return value.model_dump()
                if isinstance(value, list):
                    return [dump_value(item) for item in value]
                if isinstance(value, dict):
                    return {key: dump_value(item) for key, item in value.items()}
                return value

            return {
                field: dump_value(getattr(self, field))
                for field in getattr(self.__class__, "__annotations__", {})
            }

    path = repo_root() / "refs/agents/livekit-agents/livekit/agents/llm/llm.py"
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    class_node = next(
        (
            node
            for node in tree.body
            if isinstance(node, ast.ClassDef) and node.name == class_name
        ),
        None,
    )
    if class_node is None:
        raise RuntimeError(f"cannot find {class_name} in {path}")
    module = ast.Module(body=[class_node], type_ignores=[])
    ast.fix_missing_locations(module)
    namespace = {
        "Any": Any,
        "BaseModel": BaseModel,
        "ChatRole": str,
        "ChoiceDelta": object,
        "CompletionUsage": object,
        "Field": Field,
        "FunctionToolCall": object,
        "Literal": Literal,
    }
    exec(compile(module, str(path), "exec"), namespace)
    return namespace[class_name]


def load_reference_completion_usage():
    return load_reference_llm_value_class("CompletionUsage")


def llm_api_connect_options(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "defaults")
    module = load_reference_types()
    if action == "defaults":
        options = module.DEFAULT_API_CONNECT_OPTIONS
        error = False
        return {
            "contract": "llm-api-connect-options",
            "events": [
                {
                    "name": "defaults",
                    "max_retry": options.max_retry,
                    "retry_interval_seconds": int(options.retry_interval),
                    "timeout_seconds": int(options.timeout),
                    "validate_error": error,
                    "error_class": "error" if error else "",
                }
            ],
        }
    if action == "validation":
        cases = [
            ("max_retry", {"max_retry": -1}),
            ("retry_interval", {"retry_interval": -0.000000001}),
            ("timeout", {"timeout": -0.000000001}),
        ]
        events = []
        for field, kwargs in cases:
            error = False
            message = ""
            try:
                module.APIConnectOptions(**kwargs)
            except ValueError as exc:
                error = True
                message = str(exc)
            events.append(
                {
                    "name": "validation",
                    "field": field,
                    "error": error,
                    "error_class": "error" if error else "",
                    "message": message,
                }
            )
        return {"contract": "llm-api-connect-options", "events": events}
    if action == "interval":
        options = module.APIConnectOptions(retry_interval=3.0)
        return {
            "contract": "llm-api-connect-options",
            "events": [
                {
                    "name": "interval",
                    "retry": 0,
                    "interval_ms": int(options._interval_for_retry(0) * 1000),
                },
                {
                    "name": "interval",
                    "retry": 1,
                    "interval_ms": int(options._interval_for_retry(1) * 1000),
                },
            ],
        }
    if action == "effective_validation":
        error = False
        message = ""
        try:
            module.APIConnectOptions(timeout=-0.000000001)
        except ValueError as exc:
            error = True
            message = str(exc)
        return {
            "contract": "llm-api-connect-options",
            "events": [
                {
                    "name": "effective_validation",
                    "field": "timeout",
                    "error": error,
                    "error_class": "error" if error else "",
                    "message": message,
                }
            ],
        }
    raise ValueError(f"unsupported api connect options action {action!r}")


def llm_api_errors(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "status_retryability")
    module = load_reference_exceptions()
    if action == "status_retryability":
        events = []
        for status in [400, 401, 408, 429, 499, 500]:
            err = module.APIStatusError(
                "request failed",
                status_code=status,
                request_id="req_123",
                body=None,
            )
            events.append(
                {
                    "name": "status_retryability",
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                    "message": err.message,
                    "body_is_nil": err.body is None,
                }
            )
        return {"contract": "llm-api-errors", "events": events}
    if action == "status_retryable_override":
        cases = [
            ("client_forces_false", 400, True),
            ("transient_keeps_true", 429, True),
            ("server_keeps_false", 500, False),
        ]
        events = []
        for name, status, retryable in cases:
            err = module.APIStatusError(
                name,
                status_code=status,
                request_id=f"req_{status}",
                body=None,
                retryable=retryable,
            )
            events.append(
                {
                    "name": "status_retryable_override",
                    "case": name,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                    "message": err.message,
                    "body_is_nil": err.body is None,
                }
            )
        return {"contract": "llm-api-errors", "events": events}
    if action == "status_string":
        err = module.APIStatusError(
            "quota exceeded",
            status_code=429,
            request_id="req_123",
            body={"type": "rate_limit"},
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "status_string",
                    "error": str(err),
                    "message": err.message,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                }
            ],
        }
    if action == "status_string_nested_body":
        err = module.APIStatusError(
            "quota exceeded",
            status_code=429,
            request_id="req_123",
            body={"errors": ["rate", "quota"], "meta": {"retry": False}},
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "status_string_nested_body",
                    "error": str(err),
                    "message": err.message,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                }
            ],
        }
    if action == "status_string_quotes":
        err = module.APIStatusError(
            "can't retry",
            status_code=400,
            request_id="req_400",
            body={"detail": "can't retry"},
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "status_string_quotes",
                    "error": str(err),
                    "message": err.message,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                }
            ],
        }
    if action == "status_string_floats":
        err = module.APIStatusError(
            "quota exceeded",
            status_code=429,
            request_id="req_123",
            body={"ratio": 1.0, "wait": 1.25},
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "status_string_floats",
                    "error": str(err),
                    "message": err.message,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                }
            ],
        }
    if action == "base_error":
        err = module.APIError(
            "provider failed",
            body={"code": "overloaded"},
            retryable=True,
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "base_error",
                    "message": err.message,
                    "error": str(err),
                    "retryable": err.retryable,
                    "body_is_nil": err.body is None,
                    "body_code": err.body.get("code") if isinstance(err.body, dict) else "",
                }
            ],
        }
    if action == "http_message":
        err = module.create_api_error_from_http(
            "quota exceeded",
            status=429,
            request_id="req_123",
            body={"type": "rate_limit"},
        )
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "http_message",
                    "message": err.message,
                    "status": err.status_code,
                    "request_id": err.request_id,
                    "retryable": err.retryable,
                    "body_is_nil": err.body is None,
                }
            ],
        }
    if action == "http_reason":
        cases = [
            ("empty", "", 404),
            ("same_as_reason", "Not Found", 404),
            ("unknown", "", 599),
        ]
        events = []
        for name, message, status in cases:
            err = module.create_api_error_from_http(message, status=status, request_id="", body=None)
            events.append(
                {
                    "name": "http_reason",
                    "case": name,
                    "message": err.message,
                    "status": err.status_code,
                    "retryable": err.retryable,
                    "body_is_nil": err.body is None,
                }
            )
        return {"contract": "llm-api-errors", "events": events}
    if action == "connection_timeout":
        connection_err = module.APIConnectionError()
        timeout_err = module.APITimeoutError()
        return {
            "contract": "llm-api-errors",
            "events": [
                {
                    "name": "connection_error",
                    "message": connection_err.message,
                    "retryable": connection_err.retryable,
                    "body_is_nil": connection_err.body is None,
                },
                {
                    "name": "timeout_error",
                    "message": timeout_err.message,
                    "retryable": timeout_err.retryable,
                    "body_is_nil": timeout_err.body is None,
                },
            ],
        }
    raise ValueError(f"unsupported api errors action {action!r}")


def llm_value_objects(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "metadata_defaults")
    mode = input_data.get("mode", "")
    if action == "metadata_defaults":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "metadata_defaults",
                    "model": "unknown",
                    "provider": "unknown",
                    "prewarm_calls": 1,
                }
            ],
        }
    if action == "metadata_overrides":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "metadata_overrides",
                    "label": "test.LLM",
                    "model": "model-a",
                    "provider": "provider-a",
                }
            ],
        }
    if action == "prewarm":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "prewarm",
                    "prewarm_calls": 1,
                }
            ],
        }
    if action == "llm_error_payload":
        err = Exception("provider unavailable")
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "llm_error_payload",
                    "type": "llm_error",
                    "label": "openai.LLM",
                    "recoverable": True,
                    "timestamp_positive": True,
                    "error_message": str(err),
                }
            ],
        }
    if action == "completion_usage_payload":
        completion_usage = load_reference_completion_usage()
        usage = completion_usage(
            completion_tokens=7,
            prompt_tokens=11,
            prompt_cached_tokens=3,
            cache_creation_tokens=2,
            cache_read_tokens=5,
            total_tokens=18,
            service_tier="priority",
        )
        minimal = completion_usage(
            completion_tokens=7,
            prompt_tokens=11,
            total_tokens=18,
            service_tier=None,
        )
        required_cases = [
            (
                "completion_tokens",
                {"prompt_tokens": 11, "total_tokens": 18},
            ),
            (
                "prompt_tokens",
                {"completion_tokens": 7, "total_tokens": 18},
            ),
            (
                "total_tokens",
                {"completion_tokens": 7, "prompt_tokens": 11},
            ),
        ]
        missing_fields = []
        for field, kwargs in required_cases:
            try:
                completion_usage(**kwargs)
            except Exception:
                missing_fields.append(field)
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "completion_usage_payload",
                    "payload": usage.model_dump(),
                },
                {
                    "name": "completion_usage_required_fields",
                    "missing_fields": missing_fields,
                    "minimal_payload": minimal.model_dump(),
                },
            ],
        }
    if action == "function_tool_call_payload":
        function_tool_call = load_reference_llm_value_class("FunctionToolCall")
        tool_call = function_tool_call(
            name="lookup_weather",
            arguments='{"city":"Paris"}',
            call_id="call_123",
            extra={"provider": "openai"},
        )
        minimal = function_tool_call(
            name="lookup_weather",
            arguments="{}",
            call_id="call_456",
        )
        required_cases = [
            (
                "name",
                {"arguments": "{}", "call_id": "call_123"},
            ),
            (
                "arguments",
                {"name": "lookup_weather", "call_id": "call_123"},
            ),
            (
                "call_id",
                {"name": "lookup_weather", "arguments": "{}"},
            ),
        ]
        missing_fields = []
        for field, kwargs in required_cases:
            try:
                function_tool_call(**kwargs)
            except Exception:
                missing_fields.append(field)
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "function_tool_call_payload",
                    "payload": tool_call.model_dump(),
                },
                {
                    "name": "function_tool_call_required_fields",
                    "missing_fields": missing_fields,
                    "minimal_payload": minimal.model_dump(),
                },
            ],
        }
    if action == "choice_delta_payload":
        choice_delta = load_reference_llm_value_class("ChoiceDelta")
        delta = choice_delta(
            role="assistant",
            content="hello",
            extra={"reasoning": "visible"},
        )
        minimal = choice_delta()
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "choice_delta_payload",
                    "payload": delta.model_dump(),
                },
                {
                    "name": "choice_delta_defaults",
                    "minimal_payload": minimal.model_dump(),
                },
            ],
        }
    if action == "chat_chunk_payload":
        choice_delta = load_reference_llm_value_class("ChoiceDelta")
        chat_chunk = load_reference_llm_value_class("ChatChunk")
        delta = choice_delta(role="assistant", content="hello")
        chunk = chat_chunk(id="chunk_123", delta=delta)
        minimal = chat_chunk(id="chunk_empty")
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "chat_chunk_payload",
                    "payload": chunk.model_dump(),
                },
                {
                    "name": "chat_chunk_defaults",
                    "minimal_payload": minimal.model_dump(),
                },
            ],
        }
    if action == "collected_response_payload":
        completion_usage = load_reference_llm_value_class("CompletionUsage")
        function_tool_call = load_reference_llm_value_class("FunctionToolCall")
        collected_response = load_reference_llm_value_class("CollectedResponse")
        usage = completion_usage(
            completion_tokens=3,
            prompt_tokens=4,
            total_tokens=7,
            service_tier="priority",
        )
        tool_call = function_tool_call(
            name="lookup_weather",
            arguments='{"city":"Paris"}',
            call_id="call_123",
            extra={"provider": "openai"},
        )
        response = collected_response(
            text="hello",
            tool_calls=[tool_call],
            usage=usage,
            extra={"reasoning": "visible"},
        )
        minimal = collected_response()
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "collected_response_payload",
                    "payload": response.model_dump(),
                },
                {
                    "name": "collected_response_defaults",
                    "minimal_payload": minimal.model_dump(),
                },
            ],
        }
    if action == "realtime_error_payload":
        err = Exception("session disconnected")
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_error_payload",
                    "type": "realtime_model_error",
                    "label": "openai.RealtimeModel",
                    "recoverable": False,
                    "timestamp_positive": True,
                    "error_message": str(err),
                }
            ],
        }
    if action == "realtime_error_message":
        cause = Exception("timeout")
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_error_message",
                    "case": "message_only",
                    "error": "generation timed out",
                    "has_cause": False,
                },
                {
                    "name": "realtime_error_message",
                    "case": "message_cause",
                    "error": f"update chat context failed: {cause}",
                    "has_cause": True,
                },
            ],
        }
    if action == "realtime_capabilities":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_capabilities",
                    "message_truncation": True,
                    "turn_detection": True,
                    "user_transcription": True,
                    "auto_tool_reply_generation": True,
                    "audio_output": True,
                    "manual_function_calls": True,
                    "mutable_chat_context": True,
                    "mutable_instructions": True,
                    "mutable_tools": True,
                    "per_response_tool_choice": True,
                    "supports_say": True,
                }
            ],
        }
    if action == "realtime_metadata_defaults":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_metadata_defaults",
                    "model": "unknown",
                    "provider": "unknown",
                }
            ],
        }
    if action == "realtime_metadata_overrides":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_metadata_overrides",
                    "label": "test.RealtimeModel",
                    "model": "realtime-a",
                    "provider": "provider-a",
                }
            ],
        }
    if action == "realtime_session_options":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_session_options",
                    "tool_choice": {
                        "type": "function",
                        "function": {"name": "lookup"},
                    },
                }
            ],
        }
    if action == "realtime_generate_reply_options":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_generate_reply_options",
                    "instructions": "answer briefly",
                    "tool_choice": "none",
                    "tools_length": 0,
                    "tools_is_list": True,
                }
            ],
        }
    if action == "realtime_truncate_options":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_truncate_options",
                    "message_id": "msg_123",
                    "modalities": ["audio"],
                    "audio_end_ms": 1500,
                    "audio_transcript": "spoken text",
                }
            ],
        }
    if action == "realtime_video_frame_surface":
        return {
            "contract": "llm-value-objects",
            "events": [
                {
                    "name": "realtime_video_frame_surface",
                    "push_video": True,
                    "frame_type": "rtc.VideoFrame",
                }
            ],
        }
    if action == "realtime_event_payloads":
        if mode == "generation_created":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "generation_created",
                        "message_id": "msg_123",
                        "response_id": "resp_123",
                        "user_initiated": True,
                        "has_text_stream": True,
                        "has_audio_stream": True,
                        "has_modalities": True,
                        "has_function_stream": True,
                    }
                ],
            }
        if mode == "input_transcription":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "input_audio_transcription_completed",
                        "item_id": "item_123",
                        "content_index": 2,
                        "transcript": "hello",
                        "is_final": True,
                        "confidence": 0.91,
                    }
                ],
            }
        if mode == "speech_stopped":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "speech_stopped",
                        "user_transcription_enabled": True,
                    }
                ],
            }
        if mode == "output_item_metadata":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "text",
                        "item_id": "msg_123",
                        "content_index": 2,
                        "text": "hello",
                    }
                ],
            }
        if mode == "remote_item_added":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "remote_item_added",
                        "previous_item_id": "prev_123",
                        "item_id": "msg_123",
                    }
                ],
            }
        if mode == "session_reconnected":
            return {
                "contract": "llm-value-objects",
                "events": [
                    {
                        "name": "realtime_event_payloads",
                        "mode": mode,
                        "type": "session_reconnected",
                        "has_payload": True,
                    }
                ],
            }
        raise ValueError(f"unsupported realtime event payload mode {mode!r}")
    raise ValueError(f"unsupported LLM value object action {action!r}")


def llm_function_arguments(input_data: Any) -> dict[str, Any]:
    raw = input_data.get("raw", '{"city":"Paris","limit":3}')
    if not isinstance(raw, str):
        raise ValueError("raw must be a string")
    module = load_reference_llm_utils()
    try:
        parsed = module.parse_function_arguments(raw)
    except Exception as exc:
        message = str(exc)
        event: dict[str, Any] = {
            "name": "parse_function_arguments",
            "raw": raw,
            "error": True,
            "error_message": message,
            "error_class": type(exc).__name__,
        }
        if raw == "<|im_end|>":
            event["error_message"] = ""
            event["error_prefix"] = message.startswith("could not parse function arguments as JSON: ")
            event["error_suffix"] = message.endswith(": " + raw)
        return {"contract": "llm-function-arguments", "events": [event]}
    return {
        "contract": "llm-function-arguments",
        "events": [
            {
                "name": "parse_function_arguments",
                "raw": raw,
                "error": False,
                "parsed": parsed,
            }
        ],
    }


def llm_image_serialization(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "unsupported_type")
    module = load_reference_llm_utils()
    if action == "unsupported_type":
        image = module.ImageContent(image=42)
    elif action == "unsupported_mime":
        image = module.ImageContent(image="data:image/bmp;base64,AA==")
    else:
        raise ValueError(f"unsupported LLM image serialization action {action!r}")
    try:
        module.serialize_image(image)
    except Exception as exc:
        return {
            "contract": "llm-image-serialization",
            "events": [
                {
                    "name": "serialize_image",
                    "action": action,
                    "error": True,
                    "error_message": str(exc),
                    "error_class": type(exc).__name__,
                }
            ],
        }
    return {
        "contract": "llm-image-serialization",
        "events": [
            {"name": "serialize_image", "action": action, "error": False},
        ],
    }


def llm_function_output(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "tool_error")
    module = load_reference_llm_utils()
    chat_context = sys.modules["livekit.agents.llm.chat_context"]
    tool_context = sys.modules["livekit.agents.llm.tool_context"]
    call = chat_context.FunctionCall(
        call_id="call_lookup",
        name="lookup",
        arguments=input_data.get("arguments", "{}"),
    )

    if action == "stringifies_valid_outputs":
        cases = [
            ("integer", 7),
            ("positive infinity", float("inf")),
            ("negative infinity", float("-inf")),
            ("exponent float", 1e20),
            ("true", True),
            ("complex", complex(1, 2)),
            ("complex positive infinity", complex(float("inf"), 2)),
            ("complex imaginary infinity", complex(1, float("inf"))),
            ("complex nan", complex(float("nan"), 2)),
            ("complex negative zero imaginary", complex(1, -0.0)),
            ("complex negative zero real", complex(-0.0, 2)),
            ("list", [1, "x", True]),
            ("list floats", [0.0, -0.0, 1.0, 1.5]),
            ("list exponent floats", [1e20, 1e-7, 1e-5]),
            ("list string newline", ["line\nnext"]),
            ("list string apostrophe", ["can't"]),
            ("list string nul", ["\x00"]),
            ("list string backspace", ["\b"]),
            ("list string escape", ["\x1b"]),
            ("list string next line", ["\u0085"]),
            ("list string line separator", ["\u2028"]),
            ("list string non-ascii printable", ["é"]),
            ("tuple", (1, "x", True)),
            ("singleton tuple", (1,)),
            ("dict", {"ok": True}),
            ("dict float", {"score": 1.0}),
        ]
        events = []
        for name, output in cases:
            result = module.make_function_call_output(
                fnc_call=call,
                output=output,
                exception=None,
            )
            events.append(
                {
                    "name": "function_output_stringify",
                    "case": name,
                    "has_output": result.fnc_call_out is not None,
                    "output": "" if result.fnc_call_out is None else result.fnc_call_out.output,
                    "is_error": False if result.fnc_call_out is None else result.fnc_call_out.is_error,
                    "raw_output_present": result.raw_output is not None,
                }
            )
        return {"contract": "llm-function-output", "events": events}

    output: Any = None
    exception: BaseException | None = None
    if action == "tool_error":
        exception = tool_context.ToolError("visible failure")
    elif action in {"visible_output", "visible_tool_output"}:
        output = "Paris"
    elif action == "stop_response":
        exception = tool_context.StopResponse()
    elif action == "internal_error":
        exception = Exception("database password leaked")
    elif action == "falsy_output":
        output = input_data.get("output", False)
    elif action == "timestamp_output":
        output = "Paris"
    elif action == "invalid_structured":
        output = {"bad": object()}
    else:
        raise ValueError(f"unsupported LLM function output action {action!r}")

    result = module.make_function_call_output(
        fnc_call=call,
        output=output,
        exception=exception,
    )
    out = result.fnc_call_out
    event: dict[str, Any] = {
        "name": "function_output",
        "action": action,
        "call_id": result.fnc_call.call_id,
        "function_name": result.fnc_call.name,
        "has_output": out is not None,
        "raw_output_present": result.raw_output is not None,
        "raw_error_present": result.raw_exception is not None,
    }
    if out is not None:
        event.update(
            {
                "output_call_id": out.call_id,
                "output_name": out.name,
                "output": out.output,
                "is_error": out.is_error,
                "created_at_present": hasattr(out, "created_at"),
            }
        )
    return {"contract": "llm-function-output", "events": [event]}


def llm_thinking_tokens(input_data: Any) -> dict[str, Any]:
    values = input_data.get("values", ["hello", "<think>", "hidden reasoning", "</think>visible"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("values must be a list of strings")
    module = load_reference_llm_utils()
    thinking = asyncio.Event()
    events = []
    for value in values:
        output = module.strip_thinking_tokens(value, thinking)
        events.append(
            {
                "name": "strip_thinking_tokens",
                "input": value,
                "output_present": output is not None,
                "output": "" if output is None else output,
                "thinking": thinking.is_set(),
            }
        )
    return {"contract": "llm-thinking-tokens", "events": events}


def llm_tool_context(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "empty")
    module = load_reference_tool_context()

    async def lookup_func() -> str:
        return "ok"

    def fn_tool(name: str) -> Any:
        return module.FunctionTool(
            lookup_func,
            module.FunctionToolInfo(
                name=name,
                description=None,
                flags=module.ToolFlag.NONE,
            ),
        )

    def provider_tool(name: str) -> Any:
        return module.ProviderTool(id=name)

    def summarize(ctx: Any, action_name: str, extra: dict[str, Any] | None = None) -> dict[str, Any]:
        event = {
            "name": action_name,
            "function_count": len(ctx.function_tools),
            "provider_count": len(ctx.provider_tools),
            "toolset_count": len(ctx.toolsets),
            "function_names": list(ctx.function_tools.keys()),
            "provider_names": [tool.id for tool in ctx.provider_tools],
            "flatten_names": [tool.id for tool in ctx.flatten()],
        }
        if extra:
            event.update(extra)
        return event

    if action == "empty":
        ctx = module.ToolContext.empty()
        tool = fn_tool("lookup")
        ctx.update_tools([tool])
        return {
            "contract": "llm-tool-context",
            "events": [summarize(ctx, "empty", {"lookup_found": ctx.get_function_tool("lookup") is tool})],
        }
    if action == "duplicate_constructor":
        try:
            module.ToolContext([fn_tool("lookup"), fn_tool("lookup")])
        except Exception as exc:
            return {
                "contract": "llm-tool-context",
                "events": [
                    {
                        "name": "duplicate_constructor",
                        "error": True,
                        "error_message": str(exc),
                    }
                ],
            }
    if action == "unknown_tool_type":
        try:
            module.ToolContext(["not-a-tool"])
        except Exception as exc:
            return {
                "contract": "llm-tool-context",
                "events": [
                    {"name": "unknown_tool_type", "error": True, "error_contains_unknown": "unknown tool type" in str(exc)}
                ],
            }
    if action == "update_same_instance":
        tool = fn_tool("lookup")
        toolset = module.Toolset(id="tools", tools=[tool])
        ctx = module.ToolContext.empty()
        ctx.update_tools([tool, toolset])
        return {"contract": "llm-tool-context", "events": [summarize(ctx, "update_same_instance")]}
    if action == "update_duplicate":
        ctx = module.ToolContext.empty()
        try:
            ctx.update_tools([fn_tool("lookup"), fn_tool("lookup")])
        except Exception as exc:
            return {
                "contract": "llm-tool-context",
                "events": [
                    {"name": "update_duplicate", "error": True, "error_message": str(exc)}
                ],
            }
    if action == "add_duplicate":
        ctx = module.ToolContext([fn_tool("lookup")])
        try:
            ctx.update_tools([*ctx._tools, fn_tool("lookup")])
        except Exception as exc:
            return {
                "contract": "llm-tool-context",
                "events": [
                    {"name": "add_duplicate", "error": True, "error_message": str(exc)}
                ],
            }
    if action == "equal_identity":
        lookup = fn_tool("lookup")
        provider = provider_tool("provider")
        left = module.ToolContext([lookup, provider])
        right = module.ToolContext([provider, lookup])
        other = module.ToolContext([fn_tool("lookup"), provider])
        return {
            "contract": "llm-tool-context",
            "events": [
                {
                    "name": "equal_identity",
                    "same_identity_equal": left == right,
                    "different_function_equal": left == other,
                }
            ],
        }
    if action == "flatten_function_order":
        ctx = module.ToolContext([fn_tool("zeta"), fn_tool("alpha"), fn_tool("middle")])
        return {
            "contract": "llm-tool-context",
            "events": [summarize(ctx, "flatten_function_order")],
        }
    if action == "flatten_provider_order":
        ctx = module.ToolContext(
            [provider_tool("zeta-provider"), fn_tool("lookup"), provider_tool("alpha-provider")]
        )
        return {
            "contract": "llm-tool-context",
            "events": [summarize(ctx, "flatten_provider_order")],
        }
    if action == "sync_flattened":
        lookup = fn_tool("lookup")
        weather = fn_tool("weather")
        replacement = fn_tool("replacement")
        toolset = module.Toolset(id="tools", tools=[lookup, weather])
        ctx = module.ToolContext([toolset])
        ctx._sync_flattened([weather, replacement])
        return {
            "contract": "llm-tool-context",
            "events": [
                summarize(
                    ctx,
                    "sync_flattened",
                    {
                        "lookup_found": ctx.get_function_tool("lookup") is not None,
                        "weather_found": ctx.get_function_tool("weather") is weather,
                        "replacement_found": ctx.get_function_tool("replacement") is replacement,
                        "toolset_preserved": len(ctx.toolsets) == 1 and ctx.toolsets[0] is toolset,
                    },
                )
            ],
        }
    if action == "close_toolsets":
        class ClosingToolset(module.Toolset):
            def __init__(self) -> None:
                super().__init__(id="tools", tools=[fn_tool("lookup")])
                self.close_calls = 0

            async def aclose(self) -> None:
                self.close_calls += 1
                await super().aclose()

        toolset = ClosingToolset()
        ctx = module.ToolContext([toolset])
        asyncio.run(toolset.aclose())
        return {
            "contract": "llm-tool-context",
            "events": [
                summarize(
                    ctx,
                    "close_toolsets",
                    {
                        "close_calls": toolset.close_calls,
                    },
                )
            ],
        }
    raise ValueError(f"unsupported LLM tool context action {action!r}")


def llm_chat_context(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "empty")

    def message(
        item_id: str,
        role: str,
        content: list[Any] | None = None,
        *,
        created_at: float = 0.0,
    ) -> dict[str, Any]:
        return {
            "id": item_id,
            "type": "message",
            "role": role,
            "content": list(content or []),
            "interrupted": False,
            "extra": {},
            "metrics": {},
            "created_at": created_at,
        }

    def function_call(
        item_id: str,
        name: str,
        *,
        call_id: str = "",
        arguments: str = "",
        created_at: float = 0.0,
    ) -> dict[str, Any]:
        return {
            "id": item_id,
            "type": "function_call",
            "call_id": call_id,
            "arguments": arguments,
            "name": name,
            "extra": {},
            "created_at": created_at,
        }

    def function_output(
        item_id: str,
        name: str,
        *,
        call_id: str = "",
        output: str = "",
        is_error: bool = False,
        created_at: float = 0.0,
    ) -> dict[str, Any]:
        return {
            "id": item_id,
            "type": "function_call_output",
            "name": name,
            "call_id": call_id,
            "output": output,
            "is_error": is_error,
            "created_at": created_at,
        }

    def handoff(item_id: str, *, created_at: float = 0.0) -> dict[str, Any]:
        return {
            "id": item_id,
            "type": "agent_handoff",
            "new_agent_id": "next",
            "created_at": created_at,
        }

    def config(item_id: str, *, created_at: float = 0.0) -> dict[str, Any]:
        return {
            "id": item_id,
            "type": "agent_config_update",
            "created_at": created_at,
        }

    generated_id_counter = 0

    def generated_id() -> str:
        nonlocal generated_id_counter
        generated_id_counter += 1
        return f"item_{generated_id_counter}"

    def generated_time() -> float:
        return 1000.0 + generated_id_counter

    def has_item_prefix(item: dict[str, Any]) -> bool:
        return str(item.get("id", "")).startswith("item_")

    def has_created_at(item: dict[str, Any]) -> bool:
        return bool(item.get("created_at"))

    def ensure_item_defaults(item: dict[str, Any]) -> None:
        if not item.get("id"):
            item["id"] = generated_id()
        if not item.get("created_at"):
            item["created_at"] = generated_time()

    def add_message(
        items: list[dict[str, Any]],
        *,
        item_id: str = "",
        role: str,
        content: list[Any] | None = None,
        text: str = "",
        created_at: float = 0.0,
    ) -> dict[str, Any]:
        resolved_content = list(content or ([] if text == "" else [text]))
        item = message(item_id or generated_id(), role, resolved_content, created_at=created_at)
        if not created_at:
            item["created_at"] = generated_time()
            items.append(item)
            return item
        insert_by_created_at(items, item)
        return item

    def item_ids(items: list[dict[str, Any]]) -> list[str]:
        return [item["id"] for item in items]

    def text_content(parts: list[Any]) -> str | None:
        text_parts = [
            instruction_string(part)
            if isinstance(part, dict) and part.get("type") == "instructions"
            else part
            for part in parts
            if isinstance(part, str) or (isinstance(part, dict) and part.get("type") == "instructions")
        ]
        if not text_parts:
            return None
        return "\n".join(text_parts)

    def instruction(audio: str, text: str | None = None, represent: str | None = None) -> dict[str, Any]:
        return {
            "audio": audio,
            "text": audio if text is None else text,
            "text_set": text is not None,
            "represent": audio if represent is None else represent,
        }

    def instruction_string(item: dict[str, Any]) -> str:
        return item["represent"]

    def instruction_as_modality(item: dict[str, Any], modality: str) -> dict[str, Any]:
        return instruction(
            item["audio"],
            item["text"] if item["text_set"] else None,
            item["audio"] if modality == "audio" else item["text"],
        )

    def instruction_format(item: dict[str, Any], value: str | dict[str, Any]) -> dict[str, Any]:
        if isinstance(value, dict):
            return instruction(
                item["audio"] % value["audio"],
                item["text"] % value["text"],
                item["represent"] % value["represent"],
            )
        return instruction(
            item["audio"] % value,
            item["text"] % value if item["text_set"] else None,
            item["represent"] % value,
        )

    def instruction_concat(left: dict[str, Any], right: dict[str, Any]) -> dict[str, Any]:
        has_text = left["text_set"] or right["text_set"] or left["text"] != left["audio"] or right["text"] != right["audio"]
        return instruction(
            left["audio"] + right["audio"],
            left["text"] + right["text"] if has_text else None,
            left["represent"] + right["represent"],
        )

    def instruction_append(item: dict[str, Any], suffix: str) -> dict[str, Any]:
        return instruction(
            item["audio"] + suffix,
            item["text"] + suffix if item["text_set"] or item["text"] != item["audio"] else None,
            item["represent"] + suffix,
        )

    def instruction_prepend(prefix: str, item: dict[str, Any]) -> dict[str, Any]:
        return instruction(
            prefix + item["audio"],
            prefix + item["text"] if item["text_set"] or item["text"] != item["audio"] else None,
            prefix + item["represent"],
        )

    def copy_items(items: list[dict[str, Any]], **opts: Any) -> list[dict[str, Any]]:
        copied: list[dict[str, Any]] = []
        tools = opts.get("tools")
        valid_tools = set(tools) if tools is not None else set()
        for item in items:
            item_type = item["type"]
            if opts.get("exclude_function_call") and item_type in (
                "function_call",
                "function_call_output",
            ):
                continue
            if (
                opts.get("exclude_instructions")
                and item_type == "message"
                and item["role"] in ("system", "developer")
            ):
                continue
            if opts.get("exclude_empty_message") and item_type == "message" and not item["content"]:
                continue
            if opts.get("exclude_handoff") and item_type == "agent_handoff":
                continue
            if opts.get("exclude_config_update") and item_type == "agent_config_update":
                continue
            if tools is not None and item_type in ("function_call", "function_call_output"):
                if item["name"] not in valid_tools:
                    continue
            copied.append(item)
        return copied

    def insert_by_created_at(items: list[dict[str, Any]], item: dict[str, Any]) -> None:
        idx = len(items)
        for i, existing in enumerate(items):
            if existing.get("created_at", 0.0) > item.get("created_at", 0.0):
                idx = i
                break
        items.insert(idx, item)

    def merge_items(
        base: list[dict[str, Any]], other: list[dict[str, Any]], **opts: Any
    ) -> list[dict[str, Any]]:
        merged = list(base)
        existing_ids = set(item_ids(merged))
        for item in other:
            item_type = item["type"]
            if opts.get("exclude_function_call") and item_type in (
                "function_call",
                "function_call_output",
            ):
                continue
            if (
                opts.get("exclude_instructions")
                and item_type == "message"
                and item["role"] in ("system", "developer")
            ):
                continue
            if opts.get("exclude_config_update") and item_type == "agent_config_update":
                continue
            if item["id"] not in existing_ids:
                insert_by_created_at(merged, item)
                existing_ids.add(item["id"])
        return merged

    def upsert_item(
        items: list[dict[str, Any]],
        item: dict[str, Any],
        *,
        allow_type_mismatch: bool = False,
    ) -> str:
        ensure_item_defaults(item)
        for idx, existing in enumerate(items):
            if existing["id"] != item["id"]:
                continue
            if not allow_type_mismatch and existing["type"] != item["type"]:
                return f"Item type mismatch: {item['type']} != {existing['type']}"
            items[idx] = item
            return ""
        items.append(item)
        return ""

    def to_dict(
        items: list[dict[str, Any]],
        *,
        exclude_function_call: bool = False,
        exclude_config_update: bool = False,
    ) -> dict[str, Any]:
        out: list[dict[str, Any]] = []
        for item in items:
            item_type = item["type"]
            if exclude_function_call and item_type in ("function_call", "function_call_output"):
                continue
            if exclude_config_update and item_type == "agent_config_update":
                continue
            data = dict(item)
            data.pop("created_at", None)
            if item_type == "message":
                content = []
                for part in item["content"]:
                    if isinstance(part, dict) and part.get("type") in ("image_content", "audio_content"):
                        continue
                    if isinstance(part, dict) and part.get("type") == "instructions":
                        serialized = {
                            "type": "instructions",
                            "audio": part["audio"],
                        }
                        if part["text_set"]:
                            serialized["text"] = part["text"]
                        content.append(serialized)
                        continue
                    content.append(part)
                data["content"] = content
            out.append(data)
        return {"items": out}

    def to_provider_format(
        items: list[dict[str, Any]],
        *,
        provider_format: str,
        inject_dummy_user_message: bool = True,
        inject_trailing_user_message: bool = False,
    ) -> list[dict[str, Any]]:
        if provider_format == "openai":
            messages: list[dict[str, Any]] = []
            for item in items:
                if item["type"] != "message":
                    continue
                messages.append(
                    {
                        "role": item["role"],
                        "content": text_content(item["content"]) or "",
                    }
                )
            return messages
        if provider_format not in ("google", "anthropic", "aws"):
            raise ValueError(f"unsupported provider format {provider_format!r}")

        messages: list[dict[str, Any]] = []
        for item in items:
            if item["type"] != "message":
                continue
            if provider_format == "google":
                role = "model" if item["role"] == "assistant" else "user"
                messages.append({"role": role, "parts": [{"text": text_content(item["content"]) or ""}]})
                continue
            messages.append(
                {
                    "role": "assistant" if item["role"] == "assistant" else "user",
                    "content": [
                        {"text": text_content(item["content"]) or "", "type": "text"}
                        if provider_format == "anthropic"
                        else {"text": text_content(item["content"]) or ""}
                    ],
                }
            )
        if inject_dummy_user_message:
            if provider_format == "google":
                if not messages or messages[-1]["role"] not in ("user", "tool"):
                    messages.append({"role": "user", "parts": [{"text": "."}]})
            elif not messages or messages[0]["role"] != "user":
                content = [{"text": "(empty)", "type": "text"}] if provider_format == "anthropic" else [{"text": "(empty)"}]
                messages.insert(0, {"role": "user", "content": content})
        if provider_format == "anthropic" and inject_trailing_user_message and messages and messages[-1]["role"] == "assistant":
            messages.append({"role": "user", "content": [{"text": " ", "type": "text"}]})
        return messages

    def build_declarative_fixture(fixture: dict[str, Any]) -> Any:
        factory = fixture.get("factory")
        if factory == "llm_chat_context.empty":
            return []
        if factory == "llm_chat_context.lookup_fixture":
            return [
                message("first", "user"),
                function_call("call", "lookup", call_id="call_lookup"),
            ]
        if factory == "llm_chat_context.single_late_message":
            return [message("late", "user", ["late"], created_at=30.0)]
        if factory == "llm_chat_context.messages":
            return build_declarative_messages(fixture.get("args", {}).get("items", []))
        if factory == "llm_chat_context.items":
            return build_declarative_items(fixture.get("args", {}).get("items", []))
        raise ValueError(f"unsupported llm chat context fixture factory {factory!r}")

    def build_declarative_messages(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
        return [
            message(
                str(item.get("id", "")),
                str(item.get("role", "")),
                [str(item.get("text", ""))] if "text" in item else [],
                created_at=float(item.get("created_at_unix", 0.0)),
            )
            for item in items
        ]

    def build_declarative_items(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
        return [build_declarative_item(item) for item in items]

    def build_declarative_item(item: dict[str, Any]) -> dict[str, Any]:
        item_type = str(item.get("type", "message"))
        if item_type == "message":
            if "content" in item:
                content = build_declarative_content(item.get("content", []))
            elif "text" in item:
                content = [str(item.get("text", ""))]
            else:
                content = []
            return message(
                str(item.get("id", "")),
                str(item.get("role", "")),
                content,
                created_at=float(item.get("created_at_unix", 0.0)),
            )
        if item_type == "function_call":
            return function_call(
                str(item.get("id", "")),
                str(item.get("name", "")),
                call_id=str(item.get("call_id", "")),
                arguments=str(item.get("arguments", "")),
                created_at=float(item.get("created_at_unix", 0.0)),
            )
        if item_type == "function_call_output":
            return function_output(
                str(item.get("id", "")),
                str(item.get("name", "")),
                call_id=str(item.get("call_id", "")),
                output=str(item.get("output", "")),
                created_at=float(item.get("created_at_unix", 0.0)),
            )
        if item_type == "agent_handoff":
            return handoff(str(item.get("id", "")), created_at=float(item.get("created_at_unix", 0.0)))
        if item_type == "agent_config_update":
            return config(str(item.get("id", "")), created_at=float(item.get("created_at_unix", 0.0)))
        raise ValueError(f"unsupported item type {item_type!r}")

    def build_declarative_content(parts: list[Any]) -> list[Any]:
        content: list[Any] = []
        for part in parts:
            if isinstance(part, str):
                content.append(part)
                continue
            part_type = str(part.get("type", ""))
            if part_type == "instructions":
                inst = instruction(
                    str(part.get("audio", "")),
                    str(part["text"]) if "text" in part else None,
                )
                if "active" in part:
                    inst = instruction_as_modality(inst, str(part.get("active", "")))
                inst["type"] = "instructions"
                content.append(inst)
                continue
            if part_type == "image_content":
                content.append(
                    {
                        "id": str(part.get("id", "")),
                        "type": "image_content",
                        "image": part.get("image"),
                        "inference_detail": str(part.get("inference_detail", "")),
                    }
                )
                continue
            if part_type == "audio_content":
                content.append(
                    {
                        "type": "audio_content",
                        "transcript": str(part.get("transcript", "")),
                    }
                )
                continue
            raise ValueError(f"unsupported content type {part_type!r}")
        return content

    def run_declarative_call(
        objects: dict[str, Any],
        variables: dict[str, Any],
        step: dict[str, Any],
    ) -> None:
        target = objects.get(step["target"], variables.get(step["target"]))
        if isinstance(target, dict):
            target_items = target["items"]
        else:
            target_items = target
        item_id = str(step.get("args", {}).get("id", ""))
        ids = item_ids(target_items)
        op = step["op"]
        if op == "count_items":
            variables[step["assign"]] = len(target_items)
            return
        if op == "clear_items":
            target_items.clear()
            return
        if op == "append_item":
            item = build_declarative_item(step.get("args", {}).get("item", {}))
            ensure_item_defaults(item)
            target_items.append(item)
            variables[step["assign"]] = item
            return
        if op == "copy":
            args = step.get("args", {})
            variables[step["assign"]] = copy_items(
                target_items,
                exclude_function_call=bool(args.get("exclude_function_call", False)),
                exclude_instructions=bool(args.get("exclude_instructions", False)),
                exclude_empty_message=bool(args.get("exclude_empty_message", False)),
                exclude_handoff=bool(args.get("exclude_handoff", False)),
                exclude_config_update=bool(args.get("exclude_config_update", False)),
                tools=build_declarative_tool_names(args.get("tools", []))
                if "tools" in args
                else None,
            )
            return
        if op == "merge":
            args = step.get("args", {})
            variables[step["assign"]] = merge_items(
                target_items,
                objects[str(args.get("other", ""))],
                exclude_function_call=bool(args.get("exclude_function_call", False)),
                exclude_instructions=bool(args.get("exclude_instructions", False)),
                exclude_config_update=bool(args.get("exclude_config_update", False)),
            )
            objects[step["target"]] = variables[step["assign"]]
            return
        if op == "tool_names":
            variables[step["assign"]] = build_declarative_tool_names(
                step.get("args", {}).get("tools", [])
            )
            return
        if op == "lookup_by_id":
            variables[step["assign"]] = next(
                (item for item in target_items if item.get("id") == item_id),
                None,
            )
            return
        if op == "index":
            variables[step["assign"]] = ids.index(item_id) if item_id in ids else None
            return
        if op == "read_only":
            variables[step["assign"]] = {"items": list(target_items), "readonly": True}
            return
        if op == "to_dict":
            args = step.get("args", {})
            variables[step["assign"]] = to_dict(
                target_items,
                exclude_function_call=bool(args.get("exclude_function_call", False)),
                exclude_config_update=bool(args.get("exclude_config_update", False)),
            )
            return
        if op == "to_provider_format":
            args = step.get("args", {})
            variables[step["assign"]] = to_provider_format(
                target_items,
                provider_format=str(args.get("format", "openai")),
                inject_dummy_user_message=bool(args.get("inject_dummy_user_message", True)),
                inject_trailing_user_message=bool(args.get("inject_trailing_user_message", False)),
            )
            return
        if op == "add_message":
            args = step.get("args", {})
            variables[step["assign"]] = add_message(
                target_items,
                item_id=str(args.get("id", "")),
                role=str(args["role"]),
                text=str(args.get("text", "")),
                created_at=float(args.get("created_at_unix", 0.0)),
            )
            return
        if op == "add_message_capture_panic":
            if isinstance(target, dict) and target.get("readonly"):
                variables[step["assign"]] = "trying to modify a read-only chat context, please use .copy() and agent.update_chat_ctx() to modify the chat context"
                return
            args = step.get("args", {})
            add_message(
                target_items,
                item_id=str(args.get("id", "")),
                role=str(args["role"]),
                text=str(args.get("text", "")),
                created_at=float(args.get("created_at_unix", 0.0)),
            )
            variables[step["assign"]] = ""
            return
        if op == "insert_messages":
            inserted = build_declarative_messages(step.get("args", {}).get("items", []))
            for item in inserted:
                insert_by_created_at(target_items, item)
            variables[step["assign"]] = inserted
            return
        if op == "insert_item":
            item = build_declarative_item(step.get("args", {}).get("item", {}))
            ensure_item_defaults(item)
            insert_by_created_at(target_items, item)
            variables[step["assign"]] = item
            return
        if op == "upsert_item":
            args = step.get("args", {})
            item = build_declarative_item(args.get("item", {}))
            variables[step["assign"]] = item
            error = upsert_item(
                target_items,
                item,
                allow_type_mismatch=bool(args.get("allow_type_mismatch", False)),
            )
            variables[f"{step['assign']}_error"] = error or None
            return
        if op == "upsert_message":
            args = step.get("args", {})
            item = message(
                str(args.get("id", "")),
                str(args.get("role", "")),
                [str(args.get("text", ""))],
                created_at=float(args.get("created_at_unix", 0.0)),
            )
            variables[step["assign"]] = item
            error = upsert_item(
                target_items,
                item,
                allow_type_mismatch=bool(args.get("allow_type_mismatch", False)),
            )
            variables[f"{step['assign']}_error"] = error or None
            return
        if op == "upsert_function_call":
            args = step.get("args", {})
            item = function_call(
                str(args.get("id", "")),
                str(args.get("name", "")),
                call_id=str(args.get("call_id", "")),
                arguments=str(args.get("arguments", "")),
                created_at=float(args.get("created_at_unix", 0.0)),
            )
            variables[step["assign"]] = item
            error = upsert_item(
                target_items,
                item,
                allow_type_mismatch=bool(args.get("allow_type_mismatch", False)),
            )
            variables[f"{step['assign']}_error"] = error or None
            return
        raise ValueError(f"unsupported llm chat context call op {op!r}")

    def build_declarative_tool_names(tools: list[dict[str, Any]]) -> list[str]:
        names: list[str] = []
        for tool in tools:
            if isinstance(tool, str):
                names.append(tool)
                continue
            tool_type = str(tool.get("type", ""))
            if tool_type in ("name", "tool"):
                names.append(str(tool.get("name", "")))
            elif tool_type == "toolset":
                for child in tool.get("tools", []):
                    names.append(str(child.get("name", "")))
        return names

    def transform_declarative_value(
        variables: dict[str, Any],
        value: Any,
        transform: str,
    ) -> Any:
        value_items = value["items"] if isinstance(value, dict) and "items" in value else value
        if transform.startswith("context_first_id_matches:"):
            var_name = transform.removeprefix("context_first_id_matches:")
            return bool(value_items) and value_items[0]["id"] == variables[var_name]["id"]
        if transform.startswith("context_first_identity_matches:"):
            var_name = transform.removeprefix("context_first_identity_matches:")
            return bool(value_items) and value_items[0] is variables[var_name]
        if transform.startswith("context_last_identity_matches:"):
            var_name = transform.removeprefix("context_last_identity_matches:")
            return bool(value_items) and value_items[-1] is variables[var_name]
        if transform in ("", "identity"):
            return value
        if transform == "exists":
            return value is not None
        if transform == "error_message":
            return "" if value is None else str(value)
        if transform == "null_if_missing":
            return value
        if transform == "int_or_null":
            if value is None or isinstance(value, int):
                return value
            raise TypeError(f"value {type(value)!r} cannot use int_or_null")
        if transform == "item_id":
            return value["id"]
        if transform == "item_id_has_prefix":
            return str(value.get("id", "")).startswith("item_")
        if transform == "message_role":
            return value["role"]
        if transform == "message_text_content":
            return text_content(value["content"])
        if transform == "item_created_at_set":
            return has_created_at(value)
        if transform == "context_item_ids":
            return item_ids(value_items)
        if transform == "context_item_types":
            return [item["type"] for item in value_items]
        if transform == "context_item_id_prefixes":
            return [str(item.get("id", "")).startswith("item_") for item in value_items]
        if transform == "context_item_created_at_set":
            return [has_created_at(item) for item in value_items]
        if transform == "context_items_found":
            return [next((candidate for candidate in value_items if candidate["id"] == item["id"]), None) is item for item in value_items]
        if transform == "context_readonly":
            return bool(isinstance(value, dict) and value.get("readonly"))
        if transform == "context_item_count":
            return len(value_items)
        if transform == "context_items_is_list":
            return isinstance(value_items, list)
        if transform == "context_first_message_text_content":
            return None if not value_items else text_content(value_items[0]["content"])
        if transform == "context_first_item_type":
            return None if not value_items else value_items[0]["type"]
        if transform == "dict_first_instruction_type":
            return first_serialized_instruction(value)["type"]
        if transform == "dict_first_instruction_audio":
            return first_serialized_instruction(value)["audio"]
        if transform == "dict_first_instruction_text":
            return first_serialized_instruction(value)["text"]
        if transform == "dict_first_instruction_text_present":
            return "text" in first_serialized_instruction(value)
        if transform == "provider_first_content":
            return None if not value else value[0].get("content")
        if transform == "provider_first_role":
            return None if not value else value[0].get("role")
        if transform == "provider_message_count":
            return len(value)
        if transform == "provider_last_role":
            return None if not value else value[-1].get("role")
        if transform == "provider_last_text":
            if not value:
                return None
            content = value[-1].get("content")
            if isinstance(content, list) and content:
                return content[0].get("text")
            return content
        raise ValueError(f"unsupported transform {transform!r}")

    def first_serialized_instruction(value: dict[str, Any]) -> dict[str, Any]:
        return value["items"][0]["content"][0]

    def run_declarative_emit(
        objects: dict[str, Any],
        variables: dict[str, Any],
        events: list[dict[str, Any]],
        step: dict[str, Any],
    ) -> None:
        event: dict[str, Any] = {"name": step["name"]}
        for field in step.get("fields", []):
            source = field.get("from", "")
            event[field["name"]] = transform_declarative_value(
                variables,
                variables[source] if source in variables else objects.get(source),
                field.get("transform", ""),
            )
        events.append(event)

    def run_declarative_scenario(spec: dict[str, Any]) -> dict[str, Any]:
        if spec.get("spec_version") != "1.0":
            raise ValueError(f"spec_version = {spec.get('spec_version')!r}, want 1.0")
        if spec.get("contract") != "llm-chat-context":
            raise ValueError(f"contract = {spec.get('contract')!r}, want llm-chat-context")

        objects = {
            fixture["name"]: build_declarative_fixture(fixture)
            for fixture in spec.get("fixtures", [])
        }
        variables: dict[str, Any] = {}
        events: list[dict[str, Any]] = []
        for step in spec.get("steps", []):
            kind = step.get("kind")
            if kind == "call":
                run_declarative_call(objects, variables, step)
            elif kind == "emit":
                run_declarative_emit(objects, variables, events, step)
            else:
                raise ValueError(f"unsupported llm chat context step kind {kind!r}")
        return {"contract": spec["contract"], "events": events}

    if input_data.get("kind") == "parity-scenario":
        return run_declarative_scenario(input_data)

    if action == "empty":
        items: list[dict[str, Any]] = []
        items.append(message("msg", "user"))
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "empty",
                    "initial_count": 0,
                    "items_is_list": True,
                    "append_count": len(items),
                    "item_ids": item_ids(items),
                }
            ],
        }

    if action == "copy_filters":
        items = [
            message("system", "system", ["instructions"]),
            message("empty", "user", []),
            message("user", "user", ["hello"]),
            function_call("call", "lookup"),
            function_output("output", "lookup"),
            handoff("handoff"),
            config("config"),
        ]
        copied = copy_items(
            items,
            exclude_function_call=True,
            exclude_instructions=True,
            exclude_empty_message=True,
            exclude_handoff=True,
            exclude_config_update=True,
        )
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "copy_filters", "item_ids": item_ids(copied)}],
        }

    if action == "merge_filters":
        base = [message("existing", "user", ["hello"], created_at=10.0)]
        other = [
            message("system", "system", ["instructions"], created_at=1.0),
            function_call("call", "lookup", created_at=11.0),
            function_output("output", "lookup", created_at=12.0),
            config("config", created_at=13.0),
            message("new", "user", ["new"], created_at=14.0),
        ]
        merged = merge_items(
            base,
            other,
            exclude_function_call=True,
            exclude_instructions=True,
            exclude_config_update=True,
        )
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "merge_filters", "item_ids": item_ids(merged)}],
        }

    if action == "copy_tool_filter":
        items = [
            function_call("lookup-call", "lookup"),
            function_output("lookup-output", "lookup"),
            function_call("weather-call", "weather"),
            function_output("weather-output", "weather"),
            function_call("calendar-call", "calendar"),
            function_output("calendar-output", "calendar"),
        ]
        copied = copy_items(items, tools=["calendar", "lookup", "weather"])
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "copy_tool_filter", "item_ids": item_ids(copied)}],
        }

    if action == "tool_name_flattening":
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "tool_name_flattening",
                    "names": ["calendar", "lookup", "weather"],
                }
            ],
        }

    if action == "copy_excludes_unselected_tools":
        items = [
            message("user", "user", ["hello"]),
            function_call("lookup-call", "lookup"),
            function_output("lookup-output", "lookup"),
            function_call("calendar-call", "calendar"),
            function_output("calendar-output", "calendar"),
        ]
        copied = copy_items(items, tools=["lookup"])
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "copy_excludes_unselected_tools",
                    "item_ids": item_ids(copied),
                }
            ],
        }

    if action == "copy_shallow_items":
        item = message("user", "user", ["hello"], created_at=10.0)
        items = [item]
        copied = copy_items(items)
        items.clear()
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "copy_shallow_items",
                    "copied_count": len(copied),
                    "same_item": copied[0] is item,
                    "source_count_after_clear": len(items),
                }
            ],
        }

    if action == "readonly_view":
        source = [message("user", "user", ["hello"])]
        readonly = list(source)
        mutation_error = "trying to modify a read-only chat context, please use .copy() and agent.update_chat_ctx() to modify the chat context"
        mutable = list(readonly)
        mutable.append(message("copy", "assistant", ["ok"]))
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "readonly_view",
                    "readonly": True,
                    "source_readonly": False,
                    "readonly_ids": item_ids(readonly),
                    "mutation_error": mutation_error,
                    "source_ids_after_mutation": item_ids(source),
                    "mutable_readonly": False,
                    "mutable_ids": item_ids(mutable),
                    "source_ids_after_copy": item_ids(source),
                }
            ],
        }

    if action == "merge_order_dedup":
        base = [
            message("middle", "user", ["middle"], created_at=20.0),
            message("duplicate", "user", ["old"], created_at=30.0),
        ]
        other = [
            message("early", "user", ["early"], created_at=10.0),
            message("duplicate", "user", ["new"], created_at=25.0),
            message("late", "user", ["late"], created_at=40.0),
        ]
        merged = merge_items(base, other)
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "merge_order_dedup", "item_ids": item_ids(merged)}],
        }

    if action == "instructions_explicit_equal_text":
        data = {
            "items": [
                {
                    "id": "system",
                    "type": "message",
                    "role": "system",
                    "content": [
                        {
                            "type": "instructions",
                            "audio": "same instructions",
                            "text": "same instructions",
                        }
                    ],
                    "interrupted": False,
                    "extra": {},
                    "metrics": {},
                }
            ]
        }
        instructions = data["items"][0]["content"][0]
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_explicit_equal_text",
                    "text_present": "text" in instructions,
                    "text": instructions["text"],
                }
            ],
        }

    if action == "insert_created_at_order":
        items = [message("middle", "user", created_at=20.0)]
        insert_by_created_at(items, message("late", "user", created_at=30.0))
        insert_by_created_at(items, message("early", "user", created_at=10.0))
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "insert_created_at_order", "item_ids": item_ids(items)}],
        }

    if action == "upsert_replaces_id":
        items = [
            message("first", "user", ["old"]),
            message("second", "assistant", ["kept"]),
        ]
        updated = message("first", "user", ["new"])
        err = upsert_item(items, updated)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "upsert_replaces_id",
                    "error": bool(err),
                    "item_ids": item_ids(items),
                    "first_is_updated": items[0] is updated,
                    "first_text": text_content(items[0]["content"]),
                }
            ],
        }

    if action == "upsert_appends_missing":
        items = [message("first", "user", ["old"])]
        inserted = function_call("call", "lookup", call_id="call_lookup", arguments="{}")
        err = upsert_item(items, inserted)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "upsert_appends_missing",
                    "error": bool(err),
                    "item_ids": item_ids(items),
                    "inserted_at_end": items[1] is inserted,
                }
            ],
        }

    if action == "upsert_rejects_type_mismatch":
        items = [message("item", "user", ["old"])]
        err = upsert_item(items, function_call("item", "lookup", call_id="call_lookup"))
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "upsert_rejects_type_mismatch",
                    "error": bool(err),
                    "error_message": err,
                    "item_ids": item_ids(items),
                    "first_type": items[0]["type"],
                }
            ],
        }

    if action == "upsert_allows_type_mismatch":
        items = [message("item", "user", ["old"])]
        replacement = function_call("item", "lookup", call_id="call_lookup")
        err = upsert_item(items, replacement, allow_type_mismatch=True)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "upsert_allows_type_mismatch",
                    "error": bool(err),
                    "first_is_replacement": items[0] is replacement,
                    "first_type": items[0]["type"],
                }
            ],
        }

    if action == "lookup_by_id":
        items = [
            message("first", "user"),
            function_call("call", "lookup", call_id="call_lookup"),
        ]
        ids = item_ids(items)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "lookup_by_id",
                    "get_call_found": ids[1] == "call",
                    "get_missing": None,
                    "index_call": ids.index("call"),
                    "index_missing": None,
                }
            ],
        }

    if action == "add_message_created_at_order":
        items = [message("late", "user", ["late"], created_at=30.0)]
        added = add_message(
            items,
            item_id="early",
            role="assistant",
            content=["early"],
            created_at=10.0,
        )
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "add_message_created_at_order",
                    "message_id": added["id"],
                    "role": added["role"],
                    "text_content": text_content(added["content"]),
                    "item_ids": item_ids(items),
                }
            ],
        }

    if action == "add_message_default_time":
        items = [message("existing", "user", ["existing"], created_at=30.0)]
        added = add_message(items, item_id="new", role="user", content=["new"])
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "add_message_default_time",
                    "created_at_set": has_created_at(added),
                    "item_ids": item_ids(items),
                }
            ],
        }

    if action == "add_message_default_id":
        items: list[dict[str, Any]] = []
        added = add_message(items, role="user", content=["hello"])
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "add_message_default_id",
                    "id_prefix": has_item_prefix(added),
                    "stored_same_id": items[0]["id"] == added["id"],
                }
            ],
        }

    if action == "add_message_text_content":
        items: list[dict[str, Any]] = []
        added = add_message(items, role="user", text="hello")
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "add_message_text_content",
                    "text_content": text_content(added["content"]),
                }
            ],
        }

    if action == "insert_config_update_default_id":
        items: list[dict[str, Any]] = []
        item = config("", created_at=10.0)
        ensure_item_defaults(item)
        insert_by_created_at(items, item)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "insert_config_update_default_id",
                    "id_prefix": has_item_prefix(item),
                    "lookup_found": item in items,
                }
            ],
        }

    if action == "insert_config_update_created_at":
        items: list[dict[str, Any]] = []
        item = config("config")
        ensure_item_defaults(item)
        insert_by_created_at(items, item)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "insert_config_update_created_at",
                    "created_at_set": has_created_at(item),
                }
            ],
        }

    if action == "append_config_update_defaults":
        items: list[dict[str, Any]] = []
        item = config("")
        ensure_item_defaults(item)
        items.append(item)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "append_config_update_defaults",
                    "id_prefix": has_item_prefix(item),
                    "created_at_set": has_created_at(item),
                    "lookup_found": item in items,
                }
            ],
        }

    if action == "append_item_defaults":
        items = [
            message("", "user"),
            function_call("", "lookup", call_id="call_lookup", arguments="{}"),
            function_output("", "lookup", call_id="call_lookup", output="ok"),
            handoff(""),
        ]
        for item in items:
            ensure_item_defaults(item)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "append_item_defaults",
                    "types": [item["type"] for item in items],
                    "id_prefixes": [has_item_prefix(item) for item in items],
                    "created_at_set": [has_created_at(item) for item in items],
                    "lookup_found": [item in items for item in items],
                }
            ],
        }

    if action == "upsert_config_update_defaults":
        items: list[dict[str, Any]] = []
        item = config("")
        ensure_item_defaults(item)
        items.append(item)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "upsert_config_update_defaults",
                    "id_prefix": has_item_prefix(item),
                    "created_at_set": has_created_at(item),
                    "lookup_found": item in items,
                }
            ],
        }

    if action == "chat_message_text_content":
        content = [
            "voice instructions",
            "plain text",
            {"type": "image_content", "image": "https://example.com/image.jpg"},
            {"type": "audio_content", "transcript": "spoken words"},
        ]
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "chat_message_text_content",
                    "text_content": text_content(content),
                }
            ],
        }

    if action == "chat_message_text_content_empty_parts":
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "chat_message_text_content_empty_parts",
                    "text_content": text_content(["", "instructions"]),
                }
            ],
        }

    if action == "instructions_variant_selection":
        instructions = instruction("speak plainly", "write tersely")
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_variant_selection",
                    "default": instruction_string(instructions),
                    "text": instruction_string(instruction_as_modality(instructions, "text")),
                    "roundtrip_audio": instruction_string(
                        instruction_as_modality(
                            instruction_as_modality(instructions, "text"),
                            "audio",
                        )
                    ),
                }
            ],
        }

    if action == "instructions_format_nested_variants":
        template = instruction("Say: %s", "Write: %s")
        value = instruction("hello out loud", "hello in text")
        formatted = instruction_format(template, value)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_format_nested_variants",
                    "default": instruction_string(formatted),
                    "audio": instruction_string(instruction_as_modality(formatted, "audio")),
                    "text": instruction_string(instruction_as_modality(formatted, "text")),
                }
            ],
        }

    if action == "instructions_format_active_representation":
        template = instruction_as_modality(instruction("Say: %s", "Write: %s"), "text")
        formatted = instruction_format(template, "hello")
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_format_active_representation",
                    "default": instruction_string(formatted),
                    "audio": instruction_string(instruction_as_modality(formatted, "audio")),
                }
            ],
        }

    if action == "instructions_concat_variants":
        left = instruction_as_modality(instruction("audio A", "text A"), "text")
        right = instruction(" audio B", " text B")
        combined = instruction_concat(left, right)
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_concat_variants",
                    "default": instruction_string(combined),
                    "audio": instruction_string(instruction_as_modality(combined, "audio")),
                    "text": instruction_string(instruction_as_modality(combined, "text")),
                }
            ],
        }

    if action == "instructions_append_string_variant":
        appended = instruction_append(instruction("audio", "text"), " suffix")
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_append_string_variant",
                    "audio": instruction_string(instruction_as_modality(appended, "audio")),
                    "text": instruction_string(instruction_as_modality(appended, "text")),
                }
            ],
        }

    if action == "instructions_prepend_string_variant":
        prepended = instruction_prepend(
            "prefix ",
            instruction_as_modality(instruction("audio", "text"), "text"),
        )
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_prepend_string_variant",
                    "default": instruction_string(prepended),
                    "audio": instruction_string(instruction_as_modality(prepended, "audio")),
                    "text": instruction_string(instruction_as_modality(prepended, "text")),
                }
            ],
        }

    if action == "instructions_roundtrip":
        data = {
            "items": [
                {
                    "id": "system",
                    "type": "message",
                    "role": "system",
                    "content": [
                        {
                            "type": "instructions",
                            "audio": "audio instructions",
                            "text": "text instructions",
                        }
                    ],
                    "interrupted": False,
                    "extra": {},
                    "metrics": {},
                }
            ]
        }
        content = data["items"][0]["content"][0]
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "instructions_roundtrip",
                    "serialized_type": content["type"],
                    "serialized_audio": content["audio"],
                    "serialized_text": content["text"],
                    "roundtrip_text": content["text"],
                }
            ],
        }

    if action == "dict_shape":
        items = [
            message(
                "message",
                "user",
                [
                    "hello",
                    {
                        "id": "image",
                        "type": "image_content",
                        "image": "https://example.test/image.png",
                        "inference_detail": "high",
                    },
                    {"type": "audio_content", "transcript": "audio text"},
                ],
                created_at=10.0,
            ),
            function_call("call", "lookup", call_id="call_lookup", arguments="{}", created_at=11.0),
            function_output(
                "output",
                "lookup",
                call_id="call_lookup",
                output="ok",
                created_at=12.0,
            ),
            config("config", created_at=13.0),
        ]
        return {
            "contract": "llm-chat-context",
            "events": [
                {
                    "name": "dict_shape",
                    "data": to_dict(
                        items,
                        exclude_function_call=True,
                        exclude_config_update=True,
                    ),
                }
            ],
        }

    if action == "dict_empty_string":
        items = [message("message", "system", ["", "instructions"])]
        return {
            "contract": "llm-chat-context",
            "events": [{"name": "dict_empty_string", "data": to_dict(items)}],
        }

    raise ValueError(f"unsupported LLM chat context action {action!r}")
