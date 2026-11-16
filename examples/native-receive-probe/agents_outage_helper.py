"""Provider-free Python Agents RoomIO input using the Go outage harness's IPC.

This is a reference receiver, never a Go-native transport. SDK packages are pinned
in the experiment's lock file. Native retry/playout and Agents track rebinding
are used unchanged; no padding or receive scheduling is implemented here.
"""
import asyncio
import base64
import json
import sys
import time
from livekit import rtc
from livekit.rtc._ffi_client import FfiClient
from livekit.agents.voice.room_io._input import _ParticipantAudioInputStream


def emit(event, **data):
    print(json.dumps(dict(event=event, **data)), flush=True)


def ipc_chunks(frame):
    data = bytes(frame.data)
    if frame.sample_rate != 24000 or frame.num_channels != 1 or len(data) % 2400:
        raise RuntimeError("unsupported Agents frame shape")
    return (data[offset:offset+2400] for offset in range(0, len(data), 2400))


async def main():
    config = json.loads(sys.stdin.readline())
    room = rtc.Room()
    source = rtc.AudioSource(24000, 1, queue_size_ms=200)
    track = rtc.LocalAudioTrack.create_audio_track("agent-output", source)
    stream = _ParticipantAudioInputStream(
        room, sample_rate=24000, num_channels=1, frame_size_ms=50,
        noise_cancellation=None, auto_gain_control=False,
        pre_connect_audio_handler=None,
    )
    stream.set_track_sid(config["TrackSID"])
    stream.set_participant(config["Participant"])
    seen = False
    disconnected = False

    def trace(name):
        def callback(*args):
            print(f"python lifecycle at={time.monotonic_ns()} event={name}", file=sys.stderr, flush=True)
        return callback
    for name in ("reconnecting", "reconnected", "track_unsubscribed", "participant_disconnected", "track_published"):
        room.on(name, trace(name))

    @room.on("disconnected")
    def on_disconnected(*args):
        nonlocal disconnected
        disconnected = True
        trace("disconnected")()

    @room.on("track_subscribed")
    def subscribed(remote, publication, participant):
        nonlocal seen
        if participant.identity == config["Participant"] and publication.sid == config["TrackSID"]:
            emit("resubscribed" if seen else "subscribed")
            trace("track_subscribed")()
            seen = True

    async def consume():
        async for frame in stream:
            data=bytes(frame.data)
            print(json.dumps(dict(event="python_input_frame",at=time.monotonic_ns(),samples=frame.samples_per_channel,rate=frame.sample_rate,all_zero=not any(data))),file=sys.stderr,flush=True)
            # Framing only: preserve every framework-generated sample, with no
            # padding, pacing, concealment or inserted content of our own.
            for chunk in ipc_chunks(frame):
                emit("pcm", data=base64.b64encode(chunk).decode())

    async def control():
        while True:
            line = await asyncio.to_thread(sys.stdin.readline)
            if not line:
                raise RuntimeError("control pipe closed before stop")
            command = json.loads(line)
            event = command["event"]
            print(f"python control {event} id={command.get('id')}",file=sys.stderr,flush=True)
            if event == "stop":
                return
            if event == "capture":
                data = base64.b64decode(command["data"])
                await source.capture_frame(rtc.AudioFrame(data, 24000, 1, len(data)//2))
                emit("captured", id=command["id"])
            elif event == "clear":
                source.clear_queue()
                emit("cleared", id=command["id"])
            else:
                raise RuntimeError("unexpected control command")

    emit("duplex_protocol", version=2)
    reader = None
    publication = None
    try:
        await room.connect(config["URL"], config["Token"])
        publication = await room.local_participant.publish_track(
            track, rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE, dtx=False, red=False))
        emit("output_published")
        reader = asyncio.create_task(consume())
        controller = asyncio.create_task(control())
        done, _ = await asyncio.wait([reader, controller], return_when=asyncio.FIRST_COMPLETED)
        for task in done:
            task.result()
        if reader in done:
            raise RuntimeError("Agents input ended before stop")
    finally:
        started = time.monotonic_ns()
        if reader:
            reader.cancel()
            await asyncio.gather(reader, return_exceptions=True)
        await stream.aclose()
        await room.disconnect()
        await source.aclose()
        track._ffi_handle.dispose()
        if publication:
            publication._ffi_handle.dispose()
        room.local_participant._ffi_handle.dispose()
        room._ffi_handle.dispose()
        FfiClient.instance._dispose()
        emit("shutdown", duration_ns=time.monotonic_ns()-started,
             disconnect_callback=disconnected, retained_handles_released=True,
             runtime_disposed=True, overflow=False)


if __name__ == "__main__":
    asyncio.run(main())
