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

    class NotGivenOr:
        def __class_getitem__(cls, item):
            return object

    not_given = NotGiven()
    types_mod.NOT_GIVEN = not_given
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


def load_reference_exp_filter():
    root = repo_root()
    exp_filter_path = root / "refs/agents/livekit-agents/livekit/agents/utils/exp_filter.py"
    misc = load_reference_misc()
    utils_mod = sys.modules["livekit.agents.utils"]
    setattr(utils_mod, "misc", misc)

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.utils.exp_filter", exp_filter_path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference exp_filter.py from {exp_filter_path}")

    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.exp_filter"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_moving_average():
    root = repo_root()
    moving_average_path = root / "refs/agents/livekit-agents/livekit/agents/utils/moving_average.py"

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.utils.moving_average", moving_average_path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference moving_average.py from {moving_average_path}")

    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.moving_average"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_bounded_dict():
    root = repo_root()
    bounded_dict_path = root / "refs/agents/livekit-agents/livekit/agents/utils/bounded_dict.py"

    log_mod = types.ModuleType("livekit.agents.log")

    class Logger:
        def warning(self, *args, **kwargs):
            return None

    log_mod.logger = Logger()
    sys.modules["livekit.agents.log"] = log_mod

    spec = importlib.util.spec_from_file_location(
        "livekit.agents.utils.bounded_dict", bounded_dict_path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load reference bounded_dict.py from {bounded_dict_path}")

    module = importlib.util.module_from_spec(spec)
    sys.modules["livekit.agents.utils.bounded_dict"] = module
    spec.loader.exec_module(module)
    return module


def load_reference_tokenize_utils():
    root = repo_root()
    tokenize_root = root / "refs/agents/livekit-agents/livekit/agents/tokenize"

    utils_mod = sys.modules.setdefault("livekit.agents.utils", types.ModuleType("livekit.agents.utils"))
    aio_mod = types.ModuleType("livekit.agents.utils.aio")

    class Chan:
        def __init__(self):
            self.closed = False

        def close(self):
            self.closed = True

    aio_mod.Chan = Chan
    setattr(utils_mod, "aio", aio_mod)
    sys.modules["livekit.agents.utils.aio"] = aio_mod

    tokenize_pkg = types.ModuleType("livekit.agents.tokenize")
    tokenize_pkg.__path__ = [str(tokenize_root)]
    sys.modules["livekit.agents.tokenize"] = tokenize_pkg

    def load_module(name: str, filename: str):
        path = tokenize_root / filename
        spec = importlib.util.spec_from_file_location(name, path)
        if spec is None or spec.loader is None:
            raise RuntimeError(f"cannot load {name} from {path}")
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
        return module

    tokenizer_module = load_module("livekit.agents.tokenize.tokenizer", "tokenizer.py")
    setattr(tokenize_pkg, "tokenizer", tokenizer_module)
    basic_word_module = load_module("livekit.agents.tokenize._basic_word", "_basic_word.py")
    setattr(tokenize_pkg, "_basic_word", basic_word_module)
    utils_module = load_module("livekit.agents.tokenize.utils", "utils.py")
    return utils_module


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


def float_values(input_data: dict, field: str, default: list[float]) -> list[float]:
    values = input_data.get(field, default)
    if not isinstance(values, list) or not all(isinstance(value, (int, float)) for value in values):
        raise ValueError(f"{field} must be a list of numbers")
    return [float(value) for value in values]


def string_map(input_data: dict, field: str, default: dict[str, str]) -> dict[str, str]:
    values = input_data.get(field, default)
    if not isinstance(values, dict) or not all(
        isinstance(key, str) and isinstance(value, str) for key, value in values.items()
    ):
        raise ValueError(f"{field} must be an object with string keys and values")
    return dict(values)


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
            event = {
                "name": "is_hosted",
                "result": bool(misc.is_hosted()),
            }
            if value is not None:
                event["env"] = value
            events.append(event)
    finally:
        restore_env("LIVEKIT_REMOTE_EOT_URL", original, original_present)

    return {"contract": "hosted-env-presence", "events": events}


def run_cloud_url_host_suffix(input_data: dict) -> dict:
    misc = load_reference_misc()
    values = string_values(
        input_data,
        "url_values",
        [
            "wss://tenant.livekit.cloud",
            "https://tenant.livekit.run/path",
            "http://localhost:7880",
            "://bad-url",
            "https://livekit.cloud.evil.example",
        ],
    )

    events = []
    for value in values:
        events.append(
            {
                "name": "is_cloud",
                "url": value,
                "result": bool(misc.is_cloud(value)),
            }
        )

    return {"contract": "cloud-url-host-suffix", "events": events}


def run_camel_to_snake_case(input_data: dict) -> dict:
    misc = load_reference_misc()
    values = string_values(
        input_data,
        "name_values",
        [
            "HTTPServerID",
            "roomID",
            "JobContext",
            "already_ok",
            "URL",
        ],
    )

    events = []
    for value in values:
        events.append(
            {
                "name": "camel_to_snake_case",
                "input": value,
                "result": misc.camel_to_snake_case(value),
            }
        )

    return {"contract": "camel-to-snake-case", "events": events}


def run_exp_filter_initial_minimum(input_data: dict) -> dict:
    exp_filter = load_reference_exp_filter()
    alpha = float(input_data.get("alpha", 0.5))
    initial = float(input_data.get("initial", 10.0))
    minimum = float(input_data.get("min_val", 6.0))
    exp = float(input_data.get("exp", 1.0))
    sample = float(input_data.get("sample", 2.0))

    filter_ = exp_filter.ExpFilter(alpha, min_val=minimum, initial=initial)
    applied = filter_.apply(exp, sample)
    value = filter_.value

    return {
        "contract": "exp-filter-initial-minimum",
        "events": [
            {
                "name": "apply",
                "input": f"alpha={alpha:g},initial={initial:g},min={minimum:g},exp={exp:g},sample={sample:g}",
                "result": f"{applied:g}",
            },
            {
                "name": "value",
                "result": f"{value:g}",
            },
        ],
    }


def run_moving_average_window(input_data: dict) -> dict:
    moving_average = load_reference_moving_average()
    window_size = int(input_data.get("window_size", 3))
    samples = float_values(input_data, "sample_values", [1, 2, 3, 4])

    average = moving_average.MovingAverage(window_size)
    events = [
        {
            "name": "initial",
            "avg": f"{average.get_avg():g}",
            "size": average.size(),
        }
    ]
    for sample in samples:
        average.add_sample(sample)
        events.append(
            {
                "name": "add_sample",
                "sample": f"{sample:g}",
                "avg": f"{average.get_avg():g}",
                "size": average.size(),
            }
        )
    average.reset()
    events.append(
        {
            "name": "reset",
            "avg": f"{average.get_avg():g}",
            "size": average.size(),
        }
    )

    return {"contract": "moving-average-window", "events": events}


def run_bounded_dict_pop_if_order(input_data: dict) -> dict:
    bounded_dict = load_reference_bounded_dict()
    maxsize = int(input_data.get("maxsize", 4))

    dictionary = bounded_dict.BoundedDict(maxsize)
    dictionary["oldest"] = 1
    dictionary["middle"] = 2
    dictionary["newest"] = 3

    predicate_key, predicate_value = dictionary.pop_if(lambda value: value % 2 == 1)
    oldest_key, oldest_value = dictionary.pop_if()

    return {
        "contract": "bounded-dict-pop-if-order",
        "events": [
            {
                "name": "predicate_odd",
                "result": {
                    "key": predicate_key,
                    "value": predicate_value,
                    "ok": predicate_key is not None,
                },
            },
            {
                "name": "pop_oldest",
                "result": {
                    "key": oldest_key,
                    "value": oldest_value,
                    "ok": oldest_key is not None,
                },
            },
        ],
    }


def run_tokenize_replace_words(input_data: dict) -> dict:
    tokenize_utils = load_reference_tokenize_utils()
    values = string_values(
        input_data,
        "text_values",
        ["Hello, WORLD! workflow stays.", "Do not replace flow inside workflow."],
    )
    replacements = string_map(
        input_data,
        "replacements",
        {"hello": "hi", "world": "there", "flow": "stream"},
    )

    events = []
    for value in values:
        events.append(
            {
                "name": "replace_words",
                "input": value,
                "result": tokenize_utils.replace_words(text=value, replacements=replacements),
            }
        )

    return {"contract": "tokenize-replace-words", "events": events}


def run_tokenize_split_words(input_data: dict) -> dict:
    load_reference_tokenize_utils()
    basic_word = sys.modules["livekit.agents.tokenize._basic_word"]
    values = string_values(
        input_data,
        "text_values",
        [" Hello, world!  keep-format? ", "alpha beta,gamma"],
    )
    ignore_punctuation = bool(input_data.get("ignore_punctuation", True))
    split_character = bool(input_data.get("split_character", False))
    retain_format = bool(input_data.get("retain_format", False))

    events = []
    for value in values:
        result = [
            {"token": token, "start": start, "end": end}
            for token, start, end in basic_word.split_words(
                value,
                ignore_punctuation=ignore_punctuation,
                split_character=split_character,
                retain_format=retain_format,
            )
        ]
        events.append(
            {
                "name": "split_words",
                "input": value,
                "result": result,
            }
        )

    return {"contract": "tokenize-split-words", "events": events}


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
    elif contract == "cloud-url-host-suffix":
        output = run_cloud_url_host_suffix(input_data)
    elif contract == "camel-to-snake-case":
        output = run_camel_to_snake_case(input_data)
    elif contract == "exp-filter-initial-minimum":
        output = run_exp_filter_initial_minimum(input_data)
    elif contract == "moving-average-window":
        output = run_moving_average_window(input_data)
    elif contract == "bounded-dict-pop-if-order":
        output = run_bounded_dict_pop_if_order(input_data)
    elif contract == "tokenize-replace-words":
        output = run_tokenize_replace_words(input_data)
    elif contract == "tokenize-split-words":
        output = run_tokenize_split_words(input_data)
    else:
        print(f"unsupported contract: {contract}", file=sys.stderr)
        return 2

    json.dump(output, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
