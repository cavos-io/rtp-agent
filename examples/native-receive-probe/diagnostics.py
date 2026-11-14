"""Buffered timing instrumentation; no media generation or playout policy."""
import json
import os
import threading
import time


def now_ns():
    return time.clock_gettime_ns(time.CLOCK_MONOTONIC)


def monitor(path):
    stop = threading.Event()
    ticks = []
    loads = []
    def sample():
        while True:
            due = now_ns() + 10_000_000
            if stop.wait(.01):
                return
            ticks.append([due, now_ns()])
            if len(ticks) % 100 == 0:
                loads.append([now_ns(), *os.getloadavg()])
    thread = threading.Thread(target=sample)
    thread.start()
    def finish():
        stop.set()
        thread.join()
        path.write_text(json.dumps(dict(ticks=ticks, load=loads)))
    return finish
