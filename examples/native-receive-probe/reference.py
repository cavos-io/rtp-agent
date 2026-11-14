"""Python RTC reference, using the exact same native library as Go."""
import argparse
import asyncio
import ctypes
import json
import os
import signal
import sys
from pathlib import Path
from livekit import rtc
from livekit.rtc._ffi_client import FfiClient

class Timespec(ctypes.Structure):
    _fields_ = [('tv_sec', ctypes.c_long), ('tv_nsec', ctypes.c_long)]
libc = ctypes.CDLL(None)
libc.clock_gettime.argtypes = [ctypes.c_int, ctypes.POINTER(Timespec)]
def now_ns():
    t = Timespec()
    if libc.clock_gettime(6 if sys.platform == 'darwin' else 1, ctypes.byref(t)):
        raise OSError('clock_gettime failed')
    return t.tv_sec * 1_000_000_000 + t.tv_nsec

async def run(args):
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)
    diagnostic = os.environ.get('PROBE_DIAGNOSTICS') == '1'
    callback_trace, heartbeat_trace = [], []
    queue = FfiClient.instance.queue
    original_put = queue.put
    if diagnostic:
        def traced_put(event):
            at = now_ns()
            if event.HasField('audio_stream_event') and event.audio_stream_event.HasField('frame_received'):
                audio = event.audio_stream_event
                callback_trace.append(dict(at=at, stream=audio.stream_handle,
                    samples=audio.frame_received.frame.info.samples_per_channel))
            original_put(event)
        queue.put = traced_put
    async def heartbeat():
        while not stop.is_set():
            due = now_ns() + 10_000_000
            await asyncio.sleep(.01)
            heartbeat_trace.append([due, now_ns()])
    heartbeat_task = asyncio.create_task(heartbeat()) if diagnostic else None
    room = rtc.Room()
    streams, tasks, stats_tasks = [], [], []
    offset = 0
    errors = []
    with open(args.out / 'python.jsonl', 'w', buffering=1) as log, open(args.out / 'python.pcm', 'wb') as pcm:
        def event(**data):
            log.write(json.dumps(data) + '\n')
        @room.on('track_subscribed')
        def subscribed(track, publication, participant):
            stream = rtc.AudioStream.from_track(track=track, sample_rate=24000, num_channels=1, frame_size_ms=50)
            streams.append(stream)
            event(event='subscribed', at=now_ns())
            if os.environ.get('PROBE_NATIVE_STATS') == '1':
                from google.protobuf.json_format import MessageToDict
                async def poll_stats():
                    try:
                        while not stop.is_set():
                            await asyncio.sleep(1)
                            stats = await asyncio.wait_for(track.get_stats(), 2)
                            event(event='native_stats', at=now_ns(), stats={'stats':[
                                MessageToDict(s, preserving_proto_field_name=True) for s in stats]})
                    except asyncio.CancelledError:
                        raise
                    except Exception as exc:
                        errors.append(str(exc))
                        stop.set()
                stats_tasks.append(asyncio.create_task(poll_stats()))
            async def consume():
                nonlocal offset
                try:
                    async for item in stream:
                        at = now_ns()
                        frame = item.frame
                        pcm.write(bytes(frame.data))
                        event(event='frame', at=at, samples=frame.samples_per_channel, rate=frame.sample_rate, offset=offset)
                        offset += frame.samples_per_channel
                except Exception as exc:
                    errors.append(str(exc))
                    stop.set()
            tasks.append(asyncio.create_task(consume()))
        event(event='connect_start', at=now_ns())
        try:
            await asyncio.wait_for(room.connect(args.url, os.environ['PROBE_TOKEN']), 15)
            event(event='ready', at=now_ns())
            await stop.wait()
        finally:
            stop.set()
            start = now_ns()
            for task in stats_tasks:
                task.cancel()
            await asyncio.gather(*stats_tasks, return_exceptions=True)
            for stream in streams:
                await stream.aclose()
            await asyncio.gather(*tasks)
            await room.disconnect()
            FfiClient.instance._dispose()
            if diagnostic:
                await heartbeat_task
                queue.put = original_put
                (args.out / 'python-callbacks.json').write_text(json.dumps(callback_trace))
                (args.out / 'python-heartbeat.json').write_text(json.dumps(heartbeat_trace))
            event(event='shutdown', at=now_ns(), duration_ns=now_ns()-start, errors=errors)
        if errors:
            raise RuntimeError(errors)

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--url', default='ws://127.0.0.1:18991')
    parser.add_argument('--out', type=Path, required=True)
    asyncio.run(run(parser.parse_args()))
