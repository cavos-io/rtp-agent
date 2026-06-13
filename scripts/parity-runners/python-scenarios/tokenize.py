from common import *  # noqa: F403
from common import _token_stream_next

def tokenize_token_stream(input_data: Any) -> dict[str, Any]:
    module = load_reference_token_stream()
    mode = input_data.get("mode", "closed_lifecycle")
    if not isinstance(mode, str):
        raise ValueError("mode must be a string")

    def stream_result(events: list[dict[str, Any]]) -> dict[str, Any]:
        return {
            "contract": "token-stream-" + mode.replace("_", "-"),
            "events": events,
        }

    def fields(text: str) -> list[str]:
        return text.split()

    if mode == "closed_lifecycle":
        stream = module.BufferedTokenStream(
            tokenize_fnc=fields,
            min_token_len=1,
            min_ctx_len=1,
        )
        before = stream._event_ch.closed
        error = False
        try:
            stream.end_input()
        except Exception:
            error = True
        after = stream._event_ch.closed
        return stream_result(
            [
                {"name": "closed_before", "result": before},
                {"name": "close", "error": error, "error_class": "error" if error else ""},
                {"name": "closed_after", "result": after},
            ]
        )

    if mode == "close_flush":
        stream = module.BufferedTokenStream(
            tokenize_fnc=lambda text: [text],
            min_token_len=1,
            min_ctx_len=1,
        )
        push_error = False
        close_error = False
        try:
            stream.push_text("hello")
        except Exception:
            push_error = True
        try:
            stream.end_input()
        except Exception:
            close_error = True
        return stream_result(
            [
                {
                    "name": "push_text",
                    "error": push_error,
                    "error_class": "error" if push_error else "",
                },
                {
                    "name": "close",
                    "error": close_error,
                    "error_class": "error" if close_error else "",
                },
                asyncio.run(_token_stream_next(stream)),
            ]
        )

    if mode == "next_eof_closed":
        stream = module.BufferedTokenStream(
            tokenize_fnc=lambda text: [],
            min_token_len=1,
            min_ctx_len=1,
        )
        close_error = False
        try:
            stream.end_input()
        except Exception:
            close_error = True
        return stream_result(
            [
                {
                    "name": "close",
                    "error": close_error,
                    "error_class": "error" if close_error else "",
                },
                asyncio.run(_token_stream_next(stream)),
            ]
        )

    if mode == "last_token_context":
        stream = module.BufferedTokenStream(
            tokenize_fnc=fields,
            min_token_len=1,
            min_ctx_len=1,
        )
        push_error = False
        flush_error = False
        try:
            stream.push_text("one two three")
        except Exception:
            push_error = True
        first = asyncio.run(_token_stream_next(stream))
        second = asyncio.run(_token_stream_next(stream))
        try:
            stream.flush()
        except Exception:
            flush_error = True
        third = asyncio.run(_token_stream_next(stream))
        return stream_result(
            [
                {
                    "name": "push_text",
                    "error": push_error,
                    "error_class": "error" if push_error else "",
                },
                first,
                second,
                {"name": "flush", "error": flush_error, "error_class": "error" if flush_error else ""},
                third,
            ]
        )

    if mode == "end_input_flush_close":
        stream = module.BufferedTokenStream(
            tokenize_fnc=lambda text: [text],
            min_token_len=1,
            min_ctx_len=10,
        )
        push_error = False
        end_error = False
        try:
            stream.push_text("hello")
        except Exception:
            push_error = True
        try:
            stream.end_input()
        except Exception:
            end_error = True
        return stream_result(
            [
                {
                    "name": "push_text",
                    "error": push_error,
                    "error_class": "error" if push_error else "",
                },
                {
                    "name": "end_input",
                    "error": end_error,
                    "error_class": "error" if end_error else "",
                },
                asyncio.run(_token_stream_next(stream)),
                asyncio.run(_token_stream_next(stream)),
            ]
        )

    if mode == "end_input_closed":
        stream = module.BufferedTokenStream(
            tokenize_fnc=fields,
            min_token_len=1,
            min_ctx_len=1,
        )
        first_error = False
        second_error = False
        try:
            stream.end_input()
        except Exception:
            first_error = True
        try:
            stream.end_input()
        except RuntimeError:
            second_error = True
        return stream_result(
            [
                {
                    "name": "first_end_input",
                    "error": first_error,
                    "error_class": "error" if first_error else "",
                },
                {
                    "name": "second_end_input",
                    "error": second_error,
                    "error_class": "error" if second_error else "",
                },
            ]
        )

    if mode == "aclose_no_flush":
        stream = module.BufferedTokenStream(
            tokenize_fnc=lambda text: [text],
            min_token_len=1,
            min_ctx_len=10,
        )
        push_error = False
        close_error = False
        try:
            stream.push_text("hello")
        except Exception:
            push_error = True
        try:
            asyncio.run(stream.aclose())
        except Exception:
            close_error = True
        return stream_result(
            [
                {
                    "name": "push_text",
                    "error": push_error,
                    "error_class": "error" if push_error else "",
                },
                {
                    "name": "aclose",
                    "error": close_error,
                    "error_class": "error" if close_error else "",
                },
                asyncio.run(_token_stream_next(stream)),
            ]
        )

    if mode == "whitespace_context":
        def whitespace_tokenizer(text: str) -> list[str]:
            if text.startswith("\t"):
                return ["\t", "two"]
            return text.split()

        stream = module.BufferedTokenStream(
            tokenize_fnc=whitespace_tokenizer,
            min_token_len=1,
            min_ctx_len=1,
        )
        push_error = False
        flush_error = False
        try:
            stream.push_text("one\t two")
        except Exception:
            push_error = True
        try:
            stream.flush()
        except Exception:
            flush_error = True
        return stream_result(
            [
                {
                    "name": "push_text",
                    "error": push_error,
                    "error_class": "error" if push_error else "",
                },
                {"name": "flush", "error": flush_error, "error_class": "error" if flush_error else ""},
                asyncio.run(_token_stream_next(stream)),
                asyncio.run(_token_stream_next(stream)),
            ]
        )

    raise ValueError(f"unknown token stream mode {mode}")


def tokenize_replace_words(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_tokenize_replace_words(input_data)


def tokenize_format_words(input_data: Any) -> dict[str, Any]:
    values = input_data.get("word_values", [["hello", "world"]])
    if not isinstance(values, list) or not all(
        isinstance(words, list) and all(isinstance(word, str) for word in words)
        for words in values
    ):
        raise ValueError("word_values must be a list of string lists")
    events = [
        {"name": "format_words", "input": words, "result": " ".join(words)}
        for words in values
    ]
    return {"contract": "tokenize-format-words", "events": events}


def tokenize_sentence_tokenizer(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    basic_sent = runner.load_reference_tokenize_module("_basic_sent", "_basic_sent.py")
    values = runner.string_values(
        input_data,
        "text_values",
        ["Version 1.5 is ready. Next sentence."],
    )
    events = []
    for value in values:
        result = [
            token
            for token, _start, _end in basic_sent.split_sentences(
                value,
                min_sentence_len=20,
                retain_format=False,
            )
        ]
        events.append({"name": "sentence_tokenize", "input": value, "result": result})
    return {"contract": "tokenize-sentence-tokenizer", "events": events}


def tokenize_split_words(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_tokenize_split_words(input_data)


def tokenize_split_sentences(input_data: Any) -> dict[str, Any]:
    return load_python_utils_runner().run_tokenize_split_sentences(input_data)


def tokenize_paragraphs(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    basic_paragraph = runner.load_reference_tokenize_module(
        "_basic_paragraph", "_basic_paragraph.py"
    )
    values = runner.string_values(input_data, "text_values", [" one\n\n two\nthree "])
    with_offset = bool(input_data.get("with_offset", False))
    events = []
    for value in values:
        paragraphs = basic_paragraph.split_paragraphs(value)
        if with_offset:
            result = [
                {"token": token, "start": start, "end": end}
                for token, start, end in paragraphs
            ]
            events.append({"name": "split_paragraphs", "input": value, "result": result})
        else:
            events.append(
                {
                    "name": "tokenize_paragraphs",
                    "input": value,
                    "result": [token for token, _start, _end in paragraphs],
                }
            )
    return {"contract": "tokenize-paragraphs", "events": events}


def tokenize_hyphenate_words(input_data: Any) -> dict[str, Any]:
    runner = load_python_utils_runner()
    basic_hyphenator = runner.load_reference_tokenize_module(
        "_basic_hyphenator", "_basic_hyphenator.py"
    )
    values = runner.string_values(
        input_data,
        "word_values",
        ["beautiful", "communication", "word"],
    )
    hyphenator = basic_hyphenator.Hyphenator(
        basic_hyphenator.PATTERNS,
        basic_hyphenator.EXCEPTIONS,
    )
    events = [
        {
            "name": "hyphenate_word",
            "input": value,
            "result": hyphenator.hyphenate_word(value),
        }
        for value in values
    ]
    return {"contract": "tokenize-hyphenate-words", "events": events}
