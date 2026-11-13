"""Live lifecycle checks; explicit localhost integration, outside default unit tests."""
import os
import socket
import threading
import subprocess
from pathlib import Path
from run import ROOT, records, stop, wait_ready

def run():
    os.chdir(ROOT)
    env=dict(os.environ,LIVEKIT_LIB_PATH=str(ROOT/'.cache/native/liblivekit_ffi.dylib'))
    binary=os.environ.get('PROBE_BINARY','./probe')
    root=ROOT/os.environ.get('PROBE_LIFECYCLE_OUT','results/lifecycle')
    root.mkdir(parents=True,exist_ok=True)
    # Refused connection must terminate with an error and still dispose native RTC.
    dest=root/'refused';dest.mkdir(exist_ok=True)
    with open(dest/'process.log','w') as log:
        proc=subprocess.Popen([binary,'-url','ws://127.0.0.1:18993','-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
        try:
            code=proc.wait(timeout=20)
            assert code!=0, 'refused connection falsely succeeded'
        finally:
            stop(proc)
    assert any(e['event']=='shutdown' for e in records(dest/'go.jsonl'))
    # Cancel while WebSocket establishment is stalled, before a room handle exists.
    dest=root/'connecting';dest.mkdir(exist_ok=True)
    accepted=threading.Event();release=threading.Event()
    with socket.socket() as listener:
        listener.bind(('127.0.0.1',0));listener.listen()
        def stall():
            conn,_=listener.accept()
            with conn:
                accepted.set();release.wait(10)
        thread=threading.Thread(target=stall,daemon=True);thread.start()
        with open(dest/'process.log','w') as log:
            proc=subprocess.Popen([binary,'-url',f'ws://127.0.0.1:{listener.getsockname()[1]}','-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
            try:
                assert accepted.wait(5), 'connection attempt never arrived'
                stop(proc)
                assert proc.returncode==0, 'cancellation during connect failed'
            finally:
                release.set();stop(proc);thread.join(timeout=2)
    assert any(e['event']=='shutdown' for e in records(dest/'go.jsonl'))
    # Use the full experiment's local server while it is running; join a distinct empty room.
    for i in range(3):
        dest=root/f'empty-{i}';dest.mkdir(exist_ok=True)
        with open(dest/'process.log','w') as log:
            proc=subprocess.Popen([binary,'-room',f'lifecycle-{os.getpid()}-{i}','-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
            try:
                wait_ready(proc,dest/'go.jsonl')
                stop(proc)
                assert proc.returncode==0
            finally:
                stop(proc)
        shutdown=next(e for e in records(dest/'go.jsonl') if e['event']=='shutdown')
        assert shutdown['disconnect_callback'] and shutdown['duration_ns']<2_000_000_000, shutdown
    print('refused connection, cancellation during connect, and three connected cancellation cycles passed')

if __name__=='__main__':
    run()
