"""Live lifecycle checks; explicit localhost integration, outside default unit tests."""
import os
import signal
import subprocess
import tempfile
import time
from pathlib import Path
from run import ROOT, records, stop, wait_ready

def run():
    os.chdir(ROOT)
    env=dict(os.environ,LIVEKIT_LIB_PATH=str(ROOT/'.cache/native/liblivekit_ffi.dylib'))
    root=ROOT/'results/lifecycle'
    root.mkdir(parents=True,exist_ok=True)
    # Refused connection must terminate with an error and still dispose native RTC.
    dest=root/'refused';dest.mkdir(exist_ok=True)
    with open(dest/'process.log','w') as log:
        proc=subprocess.Popen(['./probe','-url','ws://127.0.0.1:18993','-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
        try:
            code=proc.wait(timeout=20)
            assert code!=0, 'refused connection falsely succeeded'
        finally:
            stop(proc)
    assert any(e['event']=='shutdown' for e in records(dest/'go.jsonl'))
    # Use the full experiment's local server while it is running; join a distinct empty room.
    for i in range(3):
        dest=root/f'empty-{i}';dest.mkdir(exist_ok=True)
        with open(dest/'process.log','w') as log:
            proc=subprocess.Popen(['./probe','-room',f'lifecycle-{os.getpid()}-{i}','-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
            try:
                wait_ready(proc,dest/'go.jsonl')
                stop(proc)
                assert proc.returncode==0
            finally:
                stop(proc)
        shutdown=next(e for e in records(dest/'go.jsonl') if e['event']=='shutdown')
        assert shutdown['disconnect_callback'] and shutdown['duration_ns']<2_000_000_000, shutdown
    print('refused connection and three connected cancellation cycles passed')

if __name__=='__main__':
    run()
