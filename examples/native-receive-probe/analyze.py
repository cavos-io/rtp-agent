"""Analyze recorded PCM content and shared CLOCK_MONOTONIC timestamps. Never synthesize PCM."""
import argparse
import json
from pathlib import Path
import numpy as np

def read(path):
    return [json.loads(line) for line in path.read_text().splitlines() if line]

def packet_summary(packets):
    missing=duplicates=ts_gaps=0
    for a,b in zip(packets, packets[1:]):
        delta=(b['seq']-a['seq'])%65536
        if delta==0:
            duplicates+=1
        elif delta<32768:
            missing+=delta-1
        if (b['ts']-a['ts'])%(2**32)>960:
            ts_gaps+=1
    return dict(packets=len(packets),missing_sequences=missing,duplicates=duplicates,timestamp_gaps=ts_gaps)

def find_markers(pcm):
    # 20ms FFT, 10ms hop. A strong narrow-band peak distinguishes markers from speech.
    rate=24000;size=480;hop=240
    hits=[]
    window=np.hanning(size)
    for pos in range(0,len(pcm)-size+1,hop):
        part=pcm[pos:pos+size].astype(float)
        if np.sqrt(np.mean(part*part))<1500:
            continue
        spectrum=np.abs(np.fft.rfft(part*window))**2
        peak=int(np.argmax(spectrum))
        freq=peak*rate/size
        marker=int(round((freq-600)/60))
        if not 0<=marker<20 or abs(freq-(600+marker*60))>25.1:
            continue
        ratio=spectrum[max(0,peak-1):peak+2].sum()/max(spectrum.sum(),1)
        if ratio>.93:
            hits.append((marker,pos))
    groups=[]
    for marker,pos in hits:
        if groups and groups[-1]['id']==marker and pos-groups[-1]['end']<=960:
            groups[-1]['end']=pos+size
        else:
            groups.append(dict(id=marker,start=pos,end=pos+size))
    return [g for g in groups if g['end']-g['start']>=2400]

def receiver(path,name,start,packets):
    events=read(path/f'{name}.jsonl')
    frames=[e for e in events if e['event']=='frame']
    if not frames:
        raise ValueError(f'{path}/{name}: no frames')
    pcm=np.fromfile(path/f'{name}.pcm',dtype='<i2')
    if len(pcm)!=sum(f['samples'] for f in frames):
        raise ValueError('PCM file length differs from recorded sample count')
    markers=find_markers(pcm)
    offsets=np.array([f['offset']+f['samples'] for f in frames])
    by_index={p['index']:p for p in packets}
    for m in markers:
        # Delivery timestamp of the frame containing the first detected marker sample.
        f=frames[int(np.searchsorted(offsets,m['start'],side='right'))]
        second=m['id'] if m['id']<10 else m['id']+10
        m['delivery_ns']=f['at']
        m['latency_ms']=(f['at']-by_index[second*50]['at'])/1e6
    at=np.array([f['at'] for f in frames],dtype=np.int64)
    gaps=np.diff(at)/1e6
    # Same wall-time window for both receivers; leave startup separate.
    common=[f for f in frames if start+1_000_000_000<=f['at']<start+30_000_000_000]
    duration=sum(f['samples']/f['rate'] for f in common)
    silence=[f for f in frames if start+11_000_000_000<=f['at']<start+19_000_000_000]
    shutdown=next(e for e in events if e['event']=='shutdown')
    connect=next(e for e in events if e['event']=='connect_start')
    ready=next(e for e in events if e['event']=='ready')
    second=next((m['latency_ms'] for m in markers if m['id']==10),None)
    return dict(total_pcm_s=len(pcm)/24000,window_s=29,window_pcm_s=duration,
        deficit_ms=(29-duration)*1000,silence_window_pcm_s=sum(f['samples']/f['rate'] for f in silence),
        callback_gap_p95_ms=float(np.percentile(gaps,95)),max_gap_ms=float(max(gaps)),
        gaps_over_100_ms=[float(v) for v in gaps if v>100],
        startup_ms=(at[0]-next(e['at'] for e in events if e['event']=='subscribed'))/1e6,
        premedia_frames=sum(f['at']<packets[0]['at'] for f in frames),
        first_content_ms=next((m['latency_ms'] for m in markers if m['id']==0),None),join_ms=(ready['at']-connect['at'])/1e6,
        second_onset_ms=second,markers=markers,marker_ids=[m['id'] for m in markers],
        shutdown_ms=shutdown['duration_ns']/1e6,shutdown=shutdown,
        errors=[e for e in events if e['event']=='error'])

def analyze(root):
    rows=[]
    for path in sorted(root.iterdir()):
        if not path.is_dir() or not (path/'publisher.jsonl').exists():
            continue
        pub=read(path/'publisher.jsonl')
        start=next(e for e in pub if e['event']=='start')
        packets=[p for p in pub if p['event']=='packet']
        sent=[p for p in packets if p['sent']]
        observation=read(path/'observer.jsonl')
        go=receiver(path,'go',start['at'],packets)
        py=receiver(path,'python',start['at'],packets)
        silence=[p for p in packets if 500<=p['index']<1000]
        checks=dict(duration_difference=abs(go['window_pcm_s']-py['window_pcm_s'])<=.050001,
          sustained_deficit=max(go['deficit_ms'],py['deficit_ms'])<=100.001,
          content=go['marker_ids']==py['marker_ids']==list(range(20)),
          shutdown=max(go['shutdown_ms'],py['shutdown_ms'])<=2000 and go['shutdown'].get('disconnect_callback',False),
          no_wrapper_errors=not go['errors'] and not py['errors'] and not go['shutdown'].get('overflow',False))
        rows.append(dict(run=path.name,scenario=start['scenario'],go=go,python=py,
          sent=packet_summary(sent),observer=packet_summary(observation),
          silence_packets=sum(p['sent'] for p in silence),dtx_skips=sum(p['dtx_skip'] for p in silence),
          controlled_drops=sum(p['loss'] for p in packets),
          publisher_lateness_p95_ms=float(np.percentile([(p['at']-p['scheduled'])/1e6 for p in packets],95)),
          checks=checks))
    summary={}
    for scenario in sorted({r['scenario'] for r in rows}):
        runs=[r for r in rows if r['scenario']==scenario]
        entry=dict(runs=len(runs),failed_runs=[r['run'] for r in runs if not all(r['checks'].values())])
        for metric in ['startup_ms','second_onset_ms','callback_gap_p95_ms','shutdown_ms']:
            vals={name:[r[name][metric] for r in runs] for name in ['go','python']}
            for name in vals:
                entry[f'{name}_{metric}_p95']=None if None in vals[name] else float(np.percentile(vals[name],95))
        entry['relative_latency_pass']=all(entry[f'go_{m}_p95'] is not None and entry[f'python_{m}_p95'] is not None and entry[f'go_{m}_p95']<=entry[f'python_{m}_p95']+50 for m in ['startup_ms','second_onset_ms','callback_gap_p95_ms'])
        summary[scenario]=entry
    return dict(evidence='live cross-runtime; separate receiver connections',clock='host CLOCK_MONOTONIC',runs=rows,summary=summary)

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('directory',type=Path);args=parser.parse_args()
    result=analyze(args.directory)
    (args.directory/'measurements.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result['summary'],indent=2))
