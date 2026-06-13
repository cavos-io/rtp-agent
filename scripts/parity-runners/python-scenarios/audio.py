from common import *  # noqa: F403

def audio_utils(input_data: Any) -> dict[str, Any]:
    action = input_data.get("action", "silence_shape")
    module = load_reference_audio()

    def frame_event(name: str, frame: Any, original: Any | None = None) -> dict[str, Any]:
        return {
            "name": name,
            "sample_rate": frame.sample_rate,
            "num_channels": frame.num_channels,
            "samples_per_channel": frame.samples_per_channel,
            "data_bytes": len(frame.data),
            "all_zero": all(byte == 0 for byte in frame.data),
            "duration": frame.duration,
            "same_object": frame is original if original is not None else False,
        }

    if action == "silence_shape":
        frame = module.silence_frame(0.02, 16000, 2)
        event = frame_event("silence_shape", frame)
        event["calculated_duration"] = module.calculate_audio_duration([frame])
        return {"contract": "audio-utils", "events": [event]}
    if action == "silence_zero":
        frame = module.silence_frame(0, 16000, 1)
        return {"contract": "audio-utils", "events": [frame_event("silence_zero", frame)]}
    if action == "silence_like":
        source = module.rtc.AudioFrame(
            data=bytes([1, 2, 3, 4]),
            sample_rate=16000,
            num_channels=1,
            samples_per_channel=2,
        )
        frame = module.silence_frame_like(source)
        return {"contract": "audio-utils", "events": [frame_event("silence_like", frame, source)]}
    if action == "byte_stream_default":
        stream = module.AudioByteStream(16000, 1)
        frames = stream.push(bytes(1600 * 2))
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "byte_stream_default",
                    "frame_count": len(frames),
                    "samples": [frame.samples_per_channel for frame in frames],
                }
            ],
        }
    if action == "byte_stream_write_alias":
        stream = module.AudioByteStream(16000, 1, 320)
        frames = stream.write(bytes(320 * 2))
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "byte_stream_write_alias",
                    "frame_count": len(frames),
                    "samples": [frame.samples_per_channel for frame in frames],
                }
            ],
        }
    if action == "byte_stream_progressive":
        stream = module.AudioByteStream(16000, 1, 3200, progressive=True)
        frames = stream.push(bytes((320 + 640 + 1280) * 2))
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "byte_stream_progressive",
                    "frame_count": len(frames),
                    "samples": [frame.samples_per_channel for frame in frames],
                }
            ],
        }
    if action == "byte_stream_flush_incomplete":
        stream = module.AudioByteStream(16000, 2, 1600)
        stream.push(bytes([1, 2, 3]))
        first = stream.flush()
        stream.push(bytes([4]))
        second = stream.flush()
        data = list(second[0].data) if second else []
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "byte_stream_flush_incomplete",
                    "first_frame_count": len(first),
                    "second_frame_count": len(second),
                    "second_data": data,
                }
            ],
        }
    if action == "byte_stream_clear_progressive":
        stream = module.AudioByteStream(16000, 1, 3200, progressive=True)
        stream.push(bytes(320 * 2))
        stream.clear()
        frames = stream.push(bytes(320 * 2))
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "byte_stream_clear_progressive",
                    "frame_count": len(frames),
                    "samples": [frame.samples_per_channel for frame in frames],
                }
            ],
        }
    if action == "array_buffer_reject_oversized":
        buffer = module.AudioArrayBuffer(buffer_size=2, sample_rate=16000)
        frame = module.rtc.AudioFrame(
            data=bytes(6),
            sample_rate=16000,
            num_channels=1,
            samples_per_channel=3,
        )
        error = False
        message = ""
        try:
            buffer.push_frame(frame)
        except Exception as exc:
            error = True
            message = str(exc)
        return {
            "contract": "audio-utils",
            "events": [
                {
                    "name": "array_buffer_reject_oversized",
                    "error": error,
                    "error_message": message,
                }
            ],
        }
    raise ValueError(f"unsupported audio utils action {action!r}")


