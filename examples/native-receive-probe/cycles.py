"""Run opt-in Go lifecycle tests against an owned local server and real audio track."""
import argparse
import asyncio
import json
import hashlib
import signal
import os
import socket
import subprocess
import time
from pathlib import Path
from run import ROOT, stop_all


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--cycles',type=int,default=100)
    parser.add_argument('--lifetime',choices=['process','cycle'],default='process')
    parser.add_argument('--binary',default='.cache/cycles.test')
    parser.add_argument('--publisher',action='store_true')
    parser.add_argument('--output',default='results/cycles-initial')
    args=parser.parse_args()
    if args.publisher:
        asyncio.run(publisher());return
    os.chdir(ROOT)
    out=Path(args.output).resolve();out.mkdir(parents=True,exist_ok=False)
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
        sock.bind(('127.0.0.1',18991))
    config=out/'server.yaml'
    config.write_text('port: 18991\nbind_addresses: [127.0.0.1]\nrtc:\n  use_external_ip: false\n  tcp_port: 18992\n  port_range_start: 19000\n  port_range_end: 19100\nkeys:\n  devkey: local-native-receive-experiment-secret\n')
    env=dict(os.environ,LIVEKIT_LIB_PATH=str(ROOT/'.cache/native/liblivekit_ffi.dylib'),PROBE_CYCLE_URL='ws://127.0.0.1:18991',PROBE_CYCLE_OUT=str(out),PROBE_CYCLES=str(args.cycles),PROBE_RUNTIME_LIFETIME=args.lifetime,PROBE_NATIVE_THREADS="1")
    logs=[];children=[]
    def start(command,name):
        log=open(out/(name+'.log'),'w');logs.append(log)
        p=subprocess.Popen(command,env=env,stdout=log,stderr=subprocess.STDOUT);children.append(p);return p
    try:
        server=start(['livekit-server','--config',str(config)],'server')
        for _ in range(100):
            if server.poll() is not None:raise RuntimeError('server exited')
            try:
                with socket.create_connection(('127.0.0.1',18991),timeout=.2):break
            except OSError:time.sleep(.1)
        else:raise TimeoutError('server not listening')
        env['PROBE_TOKEN']=subprocess.check_output(['./probe','-mode','token','-room','native-cycle-test'],env=env,text=True).strip()
        env['PROBE_PUBLISHER_READY']=str(out/'publisher-ready')
        pub=start(['.venv/bin/python','cycles.py','--publisher'],'publisher')
        for _ in range(100):
            if pub.poll() is not None:raise RuntimeError('publisher exited')
            if (out/'publisher-ready').exists():break
            time.sleep(.05)
        else:raise TimeoutError('publisher not ready')
        test=start([args.binary,'-test.run','^TestNativeCycles$','-test.v','-test.timeout',f'{args.cycles*3+30}s'],'cycles')
        code=test.wait(timeout=args.cycles*3+40)
        summary=summarize(out,args.lifetime,args.binary)
        summary['test_exit_code']=code
        (out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
        print(json.dumps(summary,indent=2))
        if code:raise RuntimeError(f'cycle test exited {code}; see {out}/cycles.log')
    finally:
        try:stop_all(reversed(children))
        finally:
            for log in logs:log.close()

def summarize(out,lifetime,binary=".cache/cycles.test"):
    snapshots=[json.loads(line) for line in (out/'snapshots.jsonl').read_text().splitlines()]
    baseline=snapshots[0]
    settled=[s for s in snapshots if s['name'].endswith('-settled')]
    foreign=[len(s['foreign_ids'] or []) for s in settled]
    assert len({s['pid'] for s in snapshots})==1, 'cycles did not share one PID'
    assert all(set(s['go_ids'])==set(baseline['go_ids']) for s in settled), 'Go-owned goroutine identities changed'
    assert all(s['queued_callbacks']==0 for s in settled), 'queued callbacks retained'
    shutdown=[];samples=[]
    for s in settled:
        path=out/(s['name'].removesuffix('-settled')+'.jsonl')
        events=[json.loads(line) for line in path.read_text().splitlines()]
        end=next(e for e in events if e['event']=='shutdown')
        assert end['disconnect_callback'] and end['retained_handles_released'] and not end['overflow'], end
        assert end['runtime_disposed']==(lifetime=='cycle'), end
        assert not any(e['event']=='error' for e in events)
        count=sum(e['samples'] for e in events if e['event']=='frame')
        assert count>0, 'cycle had no real PCM'
        samples.append(count);shutdown.append(end['duration_ns']/1e6)
    final=snapshots[-1]
    assert final['name']=='final-5s'
    assert set(final['go_ids'])==set(baseline['go_ids']) and final['queued_callbacks']==0
    result = dict(lifetime=lifetime,cycles=len(settled),pid=baseline['pid'],baseline_go_owned=len(baseline['go_ids']),
        final_go_owned=len(final['go_ids']),foreign_first=foreign[0],foreign_min=min(foreign),foreign_max=max(foreign),
        foreign_after_warmup=sorted(set(foreign[20:])),final_num_goroutine=final['num_goroutine'],
        final_foreign=len(final['foreign_ids'] or []),warmup_plateau_pass=all(n<=max(foreign[:20]) for n in foreign[20:]),same_go_goroutine_ids=True,queued_callbacks_max=max(s['queued_callbacks'] for s in settled),
        pcm_samples_per_cycle_min=min(samples),pcm_samples_per_cycle_max=max(samples),pcm_rate=24000,
        shutdown_ms_min=min(shutdown),shutdown_ms_max=max(shutdown),all_handles_released=True,
        test_binary_sha256=hashlib.sha256((ROOT/binary).read_bytes()).hexdigest())

    if settled[0].get('native_threads'):
        first_threads={t['id'] for t in settled[0]['native_threads']}
        first_foreign=set(settled[0]['foreign_ids'] or [])
        later=[]
        for s in settled:
            for gid in s['foreign_ids'] or []:
                if gid not in first_foreign:
                    tid=s.get('callback_threads',{}).get(gid)
                    later.append(dict(first_seen=s['name'],go_id=gid,native_thread_id=tid,
                                      thread_existed_after_first_cycle=tid in first_threads))
                    first_foreign.add(gid)
        result.update(native_threads_first=len(first_threads),
            native_threads_settled_min=min(len(s['native_threads']) for s in settled),
            native_threads_settled_max=max(len(s['native_threads']) for s in settled),
            native_threads_final=len(final['native_threads']),later_callback_contexts=later,
            final_foreign_thread_ids={g:final['callback_threads'].get(g) for g in final['foreign_ids'] or []})
    return result

async def publisher():
    import numpy as np
    from livekit import rtc
    stop=asyncio.Event()
    for sig in (signal.SIGINT,signal.SIGTERM):
        asyncio.get_running_loop().add_signal_handler(sig,stop.set)
    room=rtc.Room()
    source=rtc.AudioSource(48000,1)
    try:
        await room.connect('ws://127.0.0.1:18991',os.environ['PROBE_TOKEN'])
        track=rtc.LocalAudioTrack.create_audio_track('cycle-tone',source)
        await room.local_participant.publish_track(track,rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE,dtx=False))
        pcm=(10000*np.sin(2*np.pi*400*np.arange(960)/48000)).astype('<i2').tobytes()
        frame=rtc.AudioFrame(pcm,48000,1,960)
        Path(os.environ['PROBE_PUBLISHER_READY']).touch()
        while not stop.is_set():
            await source.capture_frame(frame)
    finally:
        await source.aclose()
        await room.disconnect()

if __name__=='__main__':main()
