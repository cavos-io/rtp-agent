from common import *  # noqa: F403
from common import _VideoFrame

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


def hosted_env_presence(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_hosted_env_presence(input_data)


def cloud_url_host_suffix(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_cloud_url_host_suffix(input_data)


def camel_to_snake_case(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_camel_to_snake_case(input_data)


def node_name_shape(input_data: Any) -> dict[str, Any]:
    name = load_reference_misc().nodename()
    return {
        "contract": "node-name-shape",
        "events": [{"name": "node_name", "non_empty": name != ""}],
    }


def shortuuid_shape(input_data: Any) -> dict[str, Any]:
    values = input_data.get("prefixes", ["prefix-"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("prefixes must be a list of strings")
    misc = load_reference_misc()
    events = []
    for prefix in values:
        value = misc.shortuuid(prefix)
        events.append(
            {
                "name": "shortuuid",
                "prefix": prefix,
                "length": len(value),
                "has_prefix": value.startswith(prefix),
            }
        )
    return {"contract": "shortuuid-shape", "events": events}


def plugin_downloader(input_data: Any) -> dict[str, Any]:
    module = load_reference_plugin()
    called = False

    class TestPlugin(module.Plugin):
        def __init__(self) -> None:
            super().__init__("title", "version", "package")

        def download_files(self) -> None:
            nonlocal called
            called = True
            raise RuntimeError("download failed")

    plugin = TestPlugin()
    module.Plugin.register_plugin(plugin)
    error = False
    try:
        plugin.download_files()
    except RuntimeError:
        error = True
    return {
        "contract": "plugin-downloader",
        "events": [
            {
                "name": "registered_plugin",
                "title": plugin.title,
                "version": plugin.version,
                "package": plugin.package,
                "download_err": error,
                "error_class": "error" if error else "",
                "called": called,
            }
        ],
    }


def language_normalize(input_data: Any) -> dict[str, Any]:
    values = input_data.get("code_values", ["ZH_hant_tw"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("code_values must be a list of strings")
    language = load_reference_language()
    events = [
        {
            "name": "normalize_language",
            "input": value,
            "result": str(language.LanguageCode(value)),
        }
        for value in values
    ]
    return {"contract": "language-normalize", "events": events}


def language_accessors(input_data: Any) -> dict[str, Any]:
    values = input_data.get("code_values", ["cmn-Hans-CN", "multi"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("code_values must be a list of strings")
    language = load_reference_language()
    events = []
    for value in values:
        code = language.LanguageCode(value)
        events.append(
            {
                "name": "language_accessors",
                "input": value,
                "normalized": str(code),
                "language": code.language,
                "iso": code.iso,
                "region": code.region or "",
                "language_name": code.to_language_name() or "",
            }
        )
    return {"contract": "language-accessors", "events": events}


def image_encode_defaults(input_data: Any) -> dict[str, Any]:
    image = load_reference_image()
    options = image.EncodeOptions()
    return {
        "contract": "image-encode-defaults",
        "events": [
            {
                "name": "encode_options",
                "format": options.format,
                "quality": options.quality,
            }
        ],
    }


def image_encode_formats(input_data: Any) -> dict[str, Any]:
    image = load_reference_image()
    values = input_data.get("formats", ["JPEG", "PNG"])
    if not isinstance(values, list) or not all(isinstance(value, str) for value in values):
        raise ValueError("formats must be a list of strings")
    frame = _VideoFrame(bytes([255, 0, 0, 255]), 1, 1)
    events = []
    for format_value in values:
        error = False
        encoded = b""
        try:
            encoded = image.encode(
                frame,
                image.EncodeOptions(format=format_value, quality=75),
            )
        except Exception:
            error = True
        events.append(
            {
                "name": "encode_format",
                "format": format_value,
                "error": error,
                "error_class": "error" if error else "",
                "non_empty": len(encoded) > 0,
            }
        )
    return {"contract": "image-encode-formats", "events": events}


def image_encode_alpha_opaque(input_data: Any) -> dict[str, Any]:
    image = load_reference_image()
    from PIL import Image
    import io

    frame = _VideoFrame(bytes([255, 0, 0, 0]), 1, 1)
    encoded = image.encode(frame, image.EncodeOptions(format="PNG"))
    decoded = Image.open(io.BytesIO(encoded)).convert("RGBA")
    r, g, b, a = decoded.getpixel((0, 0))
    return {
        "contract": "image-encode-alpha-opaque",
        "events": [
            {
                "name": "decoded_pixel",
                "r": r * 257,
                "g": g * 257,
                "b": b * 257,
                "a": a * 257,
            }
        ],
    }


def image_encode_unknown_resize(input_data: Any) -> dict[str, Any]:
    image = load_reference_image()
    from PIL import Image

    source = Image.new("RGB", (1, 1), (255, 0, 0))
    error = False
    try:
        image._resize_image(
            source,
            image.EncodeOptions(
                format="PNG",
                resize_options=image.ResizeOptions(2, 2, "unknown"),
            ),
        )
    except ValueError:
        error = True
    return {
        "contract": "image-encode-unknown-resize",
        "events": [
            {
                "name": "encode",
                "error": error,
                "error_class": "error" if error else "",
            }
        ],
    }


def exp_filter_initial_minimum(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_exp_filter_initial_minimum(input_data)


def exp_filter_reset_alpha(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    exp_filter = runner.load_reference_exp_filter()
    alpha = float(input_data.get("alpha", 0.5))
    exp = float(input_data.get("exp", 1.0))
    first = float(input_data.get("first_sample", 10.0))
    reset_alpha = float(input_data.get("reset_alpha", 0.25))
    second = float(input_data.get("second_sample", 14.0))

    filter_ = exp_filter.ExpFilter(alpha)
    first_applied = filter_.apply(exp, first)
    filter_.reset(alpha=reset_alpha)
    value = filter_.value
    second_applied = filter_.apply(exp, second)
    return {
        "contract": "exp-filter-reset-alpha",
        "events": [
            {"name": "first_apply", "result": f"{first_applied:g}"},
            {"name": "value_after_reset", "ok": value is not None, "result": f"{value:g}"},
            {"name": "second_apply", "result": f"{second_applied:g}"},
        ],
    }


def exp_filter_update_base(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    exp_filter = runner.load_reference_exp_filter()
    alpha = float(input_data.get("alpha", 0.5))
    initial = float(input_data.get("initial", 10.0))
    updated_base = float(input_data.get("updated_base", 2.0))
    exp = float(input_data.get("exp", 1.0))
    sample = float(input_data.get("sample", 14.0))

    filter_ = exp_filter.ExpFilter(alpha, initial=initial)
    filter_.update_base(updated_base)
    applied = filter_.apply(exp, sample)
    value = filter_.value
    return {
        "contract": "exp-filter-update-base",
        "events": [
            {"name": "apply", "result": f"{applied:g}"},
            {"name": "value", "ok": value is not None, "result": f"{value:g}"},
        ],
    }


def exp_filter_invalid_alpha(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    exp_filter = runner.load_reference_exp_filter()
    values = input_data.get("alpha_values", [0, 1.1])
    if not isinstance(values, list) or not all(isinstance(value, (int, float)) for value in values):
        raise ValueError("alpha_values must be a list of numbers")
    events = []
    for alpha in values:
        error = False
        try:
            exp_filter.ExpFilter(float(alpha))
        except ValueError:
            error = True
        events.append(
            {
                "name": "new_filter",
                "alpha": f"{float(alpha):g}",
                "error": error,
                "error_class": "error" if error else "",
            }
        )
    return {"contract": "exp-filter-invalid-alpha", "events": events}


def exp_filter_missing_sample(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    exp_filter = runner.load_reference_exp_filter()
    alpha = float(input_data.get("alpha", 0.5))
    exp = float(input_data.get("exp", 1.0))
    filter_ = exp_filter.ExpFilter(alpha)
    error = False
    try:
        filter_.apply(exp)
    except ValueError:
        error = True
    return {
        "contract": "exp-filter-missing-sample",
        "events": [
            {
                "name": "apply_without_sample",
                "error": error,
                "error_class": "error" if error else "",
            }
        ],
    }


def exp_filter_legacy_max_clamp(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    exp_filter = runner.load_reference_exp_filter()
    alpha = float(input_data.get("alpha", 0.5))
    maximum = float(input_data.get("max_val", 5.0))
    exp = float(input_data.get("exp", 1.0))
    sample = float(input_data.get("sample", 10.0))

    filter_ = exp_filter.ExpFilter(alpha, max_val=maximum)
    applied = filter_.apply(exp, sample)
    value = filter_.value
    return {
        "contract": "exp-filter-legacy-max-clamp",
        "events": [
            {"name": "apply", "result": f"{applied:g}"},
            {
                "name": "filtered",
                "result": f"{value:g}" if value is not None else "-1",
            },
        ],
    }


def moving_average_window(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_moving_average_window(input_data)


def bounded_dict_pop_if_order(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_bounded_dict_pop_if_order(input_data)


class _BoundedDictValue:
    def __init__(self, name: str = "", count: int = 0) -> None:
        self.name = name
        self.count = count

    def to_dict(self) -> dict[str, Any]:
        return {"name": self.name, "count": self.count}


def bounded_dict_evict_oldest(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    bounded_dict = runner.load_reference_bounded_dict()
    dictionary = bounded_dict.BoundedDict(2)
    dictionary["first"] = 1
    dictionary["second"] = 2
    first_before = "first" in dictionary
    dictionary["third"] = 3
    return {
        "contract": "bounded-dict-evict-oldest",
        "events": [
            {"name": "first_before_overflow", "ok": first_before},
            {"name": "first_after_overflow", "ok": "first" in dictionary},
            {
                "name": "second_after_overflow",
                "ok": "second" in dictionary,
                "value": dictionary.get("second", 0),
            },
            {
                "name": "third_after_overflow",
                "ok": "third" in dictionary,
                "value": dictionary.get("third", 0),
            },
        ],
    }


def bounded_dict_factory_once(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    bounded_dict = runner.load_reference_bounded_dict()
    dictionary = bounded_dict.BoundedDict(2)
    factory_calls = 0

    def first_factory() -> _BoundedDictValue:
        nonlocal factory_calls
        factory_calls += 1
        return _BoundedDictValue(name="new")

    def second_factory() -> _BoundedDictValue:
        nonlocal factory_calls
        factory_calls += 1
        return _BoundedDictValue(name="unexpected")

    first = dictionary.set_or_update("key", first_factory, count=1).to_dict()
    second = dictionary.set_or_update("key", second_factory, count=2).to_dict()
    return {
        "contract": "bounded-dict-factory-once",
        "events": [
            {"name": "factory_calls", "result": factory_calls},
            {"name": "first", "result": first},
            {"name": "second", "result": second},
        ],
    }


def bounded_dict_invalid_size(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    bounded_dict = runner.load_reference_bounded_dict()
    error = False
    try:
        bounded_dict.BoundedDict(0)
    except ValueError:
        error = True
    return {
        "contract": "bounded-dict-invalid-size",
        "events": [
            {
                "name": "new_bounded_dict",
                "error": error,
                "error_class": "error" if error else "",
            }
        ],
    }


def bounded_dict_nil_missing(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    bounded_dict = runner.load_reference_bounded_dict()
    dictionary = bounded_dict.BoundedDict(2)
    dictionary["key"] = None
    factory_calls = 0

    def factory() -> _BoundedDictValue:
        nonlocal factory_calls
        factory_calls += 1
        return _BoundedDictValue(name="fresh")

    got = dictionary.set_or_update("key", factory, count=1)
    stored = dictionary.get("key", None)
    return {
        "contract": "bounded-dict-nil-missing",
        "events": [
            {"name": "factory_calls", "result": factory_calls},
            {"name": "returned", "result": got.to_dict()},
            {"name": "stored", "ok": stored is not None, "result": stored.to_dict()},
        ],
    }


def bounded_dict_update_existing(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    bounded_dict = runner.load_reference_bounded_dict()
    dictionary = bounded_dict.BoundedDict(2)
    missing = dictionary.update_value("missing", count=1)
    dictionary["key"] = _BoundedDictValue(name="existing")
    got = dictionary.update_value("key", count=3)
    return {
        "contract": "bounded-dict-update-existing",
        "events": [
            {
                "name": "missing",
                "ok": missing is not None,
                "result": missing.to_dict() if missing is not None else {"name": "", "count": 0},
            },
            {"name": "existing", "ok": got is not None, "result": got.to_dict()},
        ],
    }


def connection_pool(input_data: Any) -> dict[str, Any]:
    mode = input_data.get("mode", "expired_close_deferred")
    if not isinstance(mode, str):
        raise ValueError("mode must be a string")
    return asyncio.run(_connection_pool_async(mode))


async def _connection_pool_async(mode: str) -> dict[str, Any]:
    module = load_reference_connection_pool()

    def result(events: list[dict[str, Any]]) -> dict[str, Any]:
        return {"contract": "connection-pool-" + mode.replace("_", "-"), "events": events}

    if mode == "expired_close_deferred":
        next_conn = 0
        close_calls = 0

        async def connect(_timeout: float) -> int:
            nonlocal next_conn
            next_conn += 1
            return next_conn

        async def close(_conn: int) -> None:
            nonlocal close_calls
            close_calls += 1
            raise RuntimeError("close failed")

        pool = module.ConnectionPool(
            max_session_duration=-1.0,
            connect_cb=connect,
            close_cb=close,
        )
        first_error = False
        fresh_error = False
        first = 0
        fresh = 0
        try:
            first = await pool.get(timeout=1.0)
        except Exception:
            first_error = True
        pool.put(first)
        try:
            fresh = await pool.get(timeout=1.0)
        except Exception:
            fresh_error = True
        return result(
            [
                {"name": "first_get", "conn": first, "error": first_error, "error_class": "error" if first_error else ""},
                {"name": "fresh_get", "conn": fresh, "error": fresh_error, "error_class": "error" if fresh_error else "", "reused": pool.last_connection_reused},
                {"name": "close_calls", "result": close_calls},
            ]
        )

    if mode == "deferred_close_error_next_get":
        next_conn = 0
        close_calls = 0

        async def connect(_timeout: float) -> int:
            nonlocal next_conn
            next_conn += 1
            return next_conn

        async def close(_conn: int) -> None:
            nonlocal close_calls
            close_calls += 1
            raise RuntimeError("close failed")

        pool = module.ConnectionPool(connect_cb=connect, close_cb=close)
        first = await pool.get(timeout=1.0)
        pool.remove(first)
        fresh = await pool.get(timeout=1.0)
        return result(
            [
                {"name": "first_get", "conn": first, "error": False, "error_class": ""},
                {"name": "fresh_get", "conn": fresh, "error": False, "error_class": "", "reused": pool.last_connection_reused},
                {"name": "close_calls", "result": close_calls},
            ]
        )

    if mode == "invalidate_close_next_get":
        next_conn = 0
        closed: list[int] = []

        async def connect(_timeout: float) -> int:
            nonlocal next_conn
            next_conn += 1
            return next_conn

        async def close(conn: int) -> None:
            closed.append(conn)

        pool = module.ConnectionPool(connect_cb=connect, close_cb=close)
        first = await pool.get(timeout=1.0)
        pool.put(first)
        pool.invalidate()
        fresh = await pool.get(timeout=1.0)
        return result(
            [
                {"name": "first_get", "conn": first, "error": False, "error_class": ""},
                {"name": "fresh_get", "conn": fresh, "error": False, "error_class": "", "reused": pool.last_connection_reused},
                {"name": "closed", "result": closed},
            ]
        )

    if mode == "remove_on_error":
        next_conn = 0
        closed: list[int] = []

        async def connect(_timeout: float) -> int:
            nonlocal next_conn
            next_conn += 1
            return next_conn

        async def close(conn: int) -> None:
            closed.append(conn)

        pool = module.ConnectionPool(connect_cb=connect, close_cb=close)
        error = False
        try:
            async with pool.connection(timeout=1.0):
                raise RuntimeError("boom")
        except RuntimeError:
            error = True
        fresh = await pool.get(timeout=1.0)
        return result(
            [
                {"name": "with_connection", "error": error, "error_class": "error" if error else ""},
                {"name": "fresh_get", "conn": fresh, "error": False, "error_class": "", "reused": pool.last_connection_reused},
                {"name": "closed", "result": closed},
            ]
        )

    if mode == "remove_on_panic":
        next_conn = 0
        closed: list[int] = []

        async def connect(_timeout: float) -> int:
            nonlocal next_conn
            next_conn += 1
            return next_conn

        async def close(conn: int) -> None:
            closed.append(conn)

        pool = module.ConnectionPool(connect_cb=connect, close_cb=close)
        error = False
        try:
            async with pool.connection(timeout=1.0):
                raise RuntimeError("boom")
        except RuntimeError:
            error = True
        fresh = await pool.get(timeout=1.0)
        return result(
            [
                {"name": "with_connection", "error": error, "error_class": "error" if error else ""},
                {"name": "fresh_get", "conn": fresh, "error": False, "error_class": "", "reused": pool.last_connection_reused},
                {"name": "closed", "result": closed},
            ]
        )

    if mode == "close_cancels_prewarm":
        started = asyncio.Event()
        canceled = asyncio.Event()

        async def connect(_timeout: float) -> int:
            started.set()
            try:
                await asyncio.sleep(3600)
            except asyncio.CancelledError:
                canceled.set()
                raise
            return 1

        pool = module.ConnectionPool(connect_cb=connect, connect_timeout=3600.0)
        pool.prewarm()
        try:
            await asyncio.wait_for(started.wait(), timeout=0.2)
            did_start = True
        except asyncio.TimeoutError:
            did_start = False
        close_error = False
        try:
            await pool.aclose()
        except Exception:
            close_error = True
        did_cancel = canceled.is_set()
        return result(
            [
                {"name": "connect_started", "result": did_start},
                {"name": "close", "error": close_error, "error_class": "error" if close_error else ""},
                {"name": "connect_canceled", "result": did_cancel},
            ]
        )

    raise ValueError(f"unknown connection pool mode {mode}")

