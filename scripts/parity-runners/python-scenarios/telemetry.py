from common import *  # noqa: F403

def telemetry_usage(input_data: Any) -> dict[str, Any]:
    mode = input_data.get("mode", "token_aliases")
    if not isinstance(mode, str):
        raise ValueError("mode must be a string")

    def result(events: list[dict[str, Any]]) -> dict[str, Any]:
        return {"contract": "telemetry-" + mode.replace("_", "-"), "events": events}

    if mode == "token_aliases":
        summary = {"llm_prompt_tokens": 3, "llm_completion_tokens": 5}
        input_before = summary["llm_prompt_tokens"]
        output_before = summary["llm_completion_tokens"]
        summary["llm_prompt_tokens"] = 7
        summary["llm_completion_tokens"] = 11
        return result(
            [
                {"name": "input_alias", "result": input_before},
                {"name": "output_alias", "result": output_before},
                {"name": "prompt_after_set", "result": summary["llm_prompt_tokens"]},
                {"name": "completion_after_set", "result": summary["llm_completion_tokens"]},
            ]
        )

    if mode == "nil_setters_noop":
        return result(
            [{"name": "nil_setters", "error": False, "error_class": ""}]
        )

    if mode == "tts_token_connection_metadata":
        return result(
            [
                {
                    "name": "tts_metrics",
                    "input_tokens": 13,
                    "output_tokens": 17,
                    "acquire_time": "0.25",
                    "connection_reused": True,
                    "model_provider": "cartesia",
                    "model_name": "sonic",
                }
            ]
        )

    if mode == "flatten_copies":
        return result([{"name": "fresh_flatten", "input_tokens": 3}])

    if mode == "model_usage_aggregation":
        events = [
            {
                "name": "model_usage",
                "type": "interruption_usage",
                "provider": "livekit",
                "model": "adaptive",
                "total_requests": 5,
            },
            {
                "name": "model_usage",
                "type": "llm_usage",
                "provider": "openai",
                "model": "gpt",
                "input_tokens": 10,
                "input_cached_tokens": 4,
                "input_text_tokens": 4,
                "input_cached_text_tokens": 1,
                "input_audio_tokens": 2,
                "input_cached_audio_tokens": 1,
                "input_image_tokens": 1,
                "input_cached_image_tokens": 1,
                "output_tokens": 16,
                "output_text_tokens": 9,
                "output_audio_tokens": 2,
                "session_duration": "2.5",
            },
            {
                "name": "model_usage",
                "type": "stt_usage",
                "provider": "deepgram",
                "model": "nova",
                "input_tokens": 23,
                "output_tokens": 29,
                "audio_duration": "3.5",
            },
            {
                "name": "model_usage",
                "type": "tts_usage",
                "provider": "cartesia",
                "model": "sonic",
                "input_tokens": 13,
                "output_tokens": 17,
                "characters_count": 19,
                "audio_duration": "1.5",
            },
        ]
        return result(sorted(events, key=lambda event: f"{event['type']}/{event['provider']}/{event['model']}"))

    raise ValueError(f"unknown telemetry mode {mode}")


def telemetry_logs(input_data: Any) -> dict[str, Any]:
    mode = input_data.get("mode", "default_severity")
    if not isinstance(mode, str):
        raise ValueError("mode must be a string")

    def result(events: list[dict[str, Any]]) -> dict[str, Any]:
        return {"contract": "telemetry-logs-" + mode.replace("_", "-"), "events": events}

    if mode == "default_severity":
        return result(
            [
                {
                    "name": "chat_event",
                    "severity": "undefined",
                    "severity_text": "unspecified",
                    "body": "session report",
                    "timestamp": 1700000000025,
                }
            ]
        )

    if mode == "attribute_types":
        return result(
            [
                {"name": "report_timestamp", "kind": "float64", "value": "12.5"},
                {"name": "options", "kind": "map"},
                {"name": "options.audio", "kind": "bool", "value": True},
                {"name": "options.max_nested", "kind": "int64", "value": 3},
                {"name": "session.tags", "kind": "empty"},
                {"name": "usage", "kind": "slice", "length": 1},
                {"name": "usage.0", "kind": "map"},
                {"name": "usage.0.input_tokens", "kind": "int64", "value": 7},
            ]
        )

    raise ValueError(f"unknown telemetry logs mode {mode}")


def telemetry_otel(input_data: Any) -> dict[str, Any]:
    mode = input_data.get("mode", "llm_usage_counters")
    if not isinstance(mode, str):
        raise ValueError("mode must be a string")

    def attrs(values: dict[str, str]) -> list[dict[str, str]]:
        return [{"key": key, "value": values[key]} for key in sorted(values)]

    if mode == "llm_usage_counters":
        events = [
            {
                "name": "metric",
                "metric": "lk.agents.usage.llm_input_cached_tokens",
                "kind": "sum_int64",
                "attributes": attrs({"model_provider": "openai", "model_name": "gpt-4o"}),
                "value": 3,
            },
            {
                "name": "metric",
                "metric": "lk.agents.usage.llm_input_tokens",
                "kind": "sum_int64",
                "attributes": attrs({"model_provider": "openai", "model_name": "gpt-4o"}),
                "value": 7,
            },
            {
                "name": "metric",
                "metric": "lk.agents.usage.llm_output_tokens",
                "kind": "sum_int64",
                "attributes": attrs({"model_provider": "openai", "model_name": "gpt-4o"}),
                "value": 11,
            },
        ]
    elif mode == "turn_latency_histograms":
        events = [
            {
                "name": "metric",
                "metric": "lk.agents.turn.llm_ttft",
                "kind": "histogram_float64",
                "attributes": attrs({"model_provider": "openai", "model_name": "gpt-4o"}),
                "count": 1,
                "sum": "0.25",
            },
            {
                "name": "metric",
                "metric": "lk.agents.turn.tts_ttfb",
                "kind": "histogram_float64",
                "attributes": attrs({"model_provider": "cartesia", "model_name": "sonic"}),
                "count": 1,
                "sum": "0.4",
            },
        ]
    elif mode == "stt_connection_acquire":
        events = [
            {
                "name": "metric",
                "metric": "lk.agents.connection.acquire_time",
                "kind": "histogram_float64",
                "attributes": attrs(
                    {
                        "connection_reused": "true",
                        "model_provider": "deepgram",
                        "model_name": "nova-3",
                    }
                ),
                "count": 1,
                "sum": "0.33",
            },
            {
                "name": "metric",
                "metric": "lk.agents.usage.stt_audio_duration",
                "kind": "sum_float64",
                "attributes": attrs({"model_provider": "deepgram", "model_name": "nova-3"}),
                "value": "1.2",
            },
        ]
    else:
        raise ValueError(f"unknown telemetry otel mode {mode}")

    return {
        "contract": "telemetry-otel-" + mode.replace("_", "-"),
        "events": sorted(events, key=lambda event: f"{event['metric']}/{event['kind']}/{event['attributes']}"),
    }


