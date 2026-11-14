"""Supplemental attribution analysis. Original analyzer and gates stay unchanged."""
import argparse
import hashlib
import json
from pathlib import Path
import numpy as np
from analyze import analyze, find_markers, read


def align_observer(packets, observer):
    """Match media across SFU header rewriting; exclude independently observed dummy packets."""
    sent = [p for p in packets if p['sent']]
    candidates = []
    for offset in range(len(observer)-len(sent)+1):
        group = observer[offset:offset+len(sent)]
        ts_offset = (group[0]['ts']-sent[0]['ts']) % 2**32
        seq_offset = (group[0]['seq']-sent[0]['seq']) % 65536
        if all((b['ts']-a['ts']) % 2**32 == ts_offset and
               (b['seq']-a['seq']) % 65536 == seq_offset and b['bytes'] == a['bytes']
               for a, b in zip(sent, group)):
            candidates.append((offset, group))
    if len(candidates) != 1:
        raise ValueError(f'observer alignment must be unique, found {len(candidates)}')
    offset, group = candidates[0]
    return sent, group, offset


def timing(values):
    return dict(p95_ms=float(np.percentile(values, 95)), max_ms=float(max(values)))


def heartbeat(path):
    if not path.exists():
        return None
    ticks = json.loads(path.read_text())
    return timing([(at-due)/1e6 for due, at in ticks]) if ticks else None


def callback_pairing(frames, callbacks):
    if len(frames) != len(callbacks) or any(f['samples'] != c['samples'] for f,c in zip(frames,callbacks)):
        raise ValueError('Python callbacks and consumed frames differ; cannot infer pairing')
    if len({c['stream'] for c in callbacks}) != 1:
        raise ValueError('expected exactly one Python audio stream')
    if any(f['at'] < c['at'] for f,c in zip(frames,callbacks)):
        raise ValueError('consumer predates callback')
    return timing([(f['at']-c['at'])/1e6 for f,c in zip(frames,callbacks)])


def native_stats(path, start):
    timeline=[]
    for event in read(path):
        if event['event']!='native_stats':
            continue
        inbound=[s['inbound_rtp'] for s in event['stats'].get('stats',[]) if 'inbound_rtp' in s and s['inbound_rtp']['stream']['kind']=='audio']
        if not inbound:  # getStats can be empty before RTP arrives.
            continue
        if len(inbound)!=1:
            raise ValueError('expected one inbound audio stats record')
        r=inbound[0]
        fields=['jitter_buffer_delay','jitter_buffer_target_delay','jitter_buffer_emitted_count',
                'concealed_samples','silent_concealed_samples','concealment_events',
                'inserted_samples_for_deceleration','removed_samples_for_acceleration','packets_discarded']
        point=dict(at_s=(event['at']-start)/1e9,
                   packets_received=int(r['received']['packets_received']),
                   packets_lost=int(r['received']['packets_lost']),
                   jitter=float(r['received']['jitter']),
                   **{k:float(r['inbound'][k]) for k in fields})
        if timeline:
            old=timeline[-1]
            emitted=point['jitter_buffer_emitted_count']-old['jitter_buffer_emitted_count']
            if emitted>0:
                point['interval_jitter_buffer_ms']=1000*(point['jitter_buffer_delay']-old['jitter_buffer_delay'])/emitted
                point['interval_target_ms']=1000*(point['jitter_buffer_target_delay']-old['jitter_buffer_target_delay'])/emitted
            point['interval_concealed_samples']=point['concealed_samples']-old['concealed_samples']
        timeline.append(point)
    return timeline


def details(path, row):
    pub = read(path/'publisher.jsonl')
    packets = [p for p in pub if p['event']=='packet']
    start = next(p['at'] for p in pub if p['event']=='start')
    result = dict(run=path.name, scenario=row['scenario'], publisher_start_ns=start,
                  publisher_lateness=timing([(p['at']-p['scheduled'])/1e6 for p in packets]))
    worst = max(packets, key=lambda p:p['at']-p['scheduled'])
    result['worst_packet'] = dict(index=worst['index'], scheduled_s=(worst['scheduled']-start)/1e9,
                                 lateness_ms=(worst['at']-worst['scheduled'])/1e6)
    sent, observed, offset = align_observer(packets, read(path/'observer.jsonl'))
    result['observer'] = dict(media_packets=len(sent), prefix_packets=offset,
        suffix_packets=len(read(path/'observer.jsonl'))-len(sent)-offset,
        transit=timing([(b['at']-a['at'])/1e6 for a,b in zip(sent,observed)]),
        max_media_gap_ms=max((b['at']-a['at'])/1e6 for a,b in zip(observed,observed[1:])))
    stages = {p['index']:p for p in pub if p['event']=='packet_stages'}
    if stages:
        result['publisher_stages'] = dict(
            wake_lateness=timing([(stages[p['index']]['awake']-p['scheduled'])/1e6 for p in packets]),
            encode=timing([(s['encode_end']-s['encode_start'])/1e6 for s in stages.values()]),
            write_rtp=timing([(stages[p['index']]['write_end']-p['at'])/1e6 for p in packets if p['sent']]))
        pcm = np.fromfile(path/'publisher.pcm',dtype='<i2')
        assert len(pcm)==30*48000, 'incomplete captured publisher input'
        result['source'] = dict(samples=len(pcm),rate=48000,
            sha256=hashlib.sha256((path/'publisher.pcm').read_bytes()).hexdigest(),
            marker_ids=[m['id'] for m in find_markers(pcm[::2])])
        assert result['source']['marker_ids']==list(range(20)), 'source marker construction failed'
    result['injections'] = [p for p in pub if p['event']=='injected_stall']
    for name in ['go','python']:
        frames = [f for f in read(path/f'{name}.jsonl') if f['event']=='frame']
        markers = row[name]['markers']
        onset = next(m for m in markers if m['id']==10)
        idx = next(i for i,f in enumerate(frames) if f['offset']+f['samples']>onset['start'])
        f = frames[idx]
        data = dict(marker_ids=row[name]['marker_ids'], second_onset_ms=row[name]['second_onset_ms'],
            max_delivery_gap_ms=row[name]['max_gap_ms'],heartbeat=heartbeat(path/f'{name}-heartbeat.json'),
            marker8_regions=[dict(start_s=m['start']/24000,duration_ms=(m['end']-m['start'])/24) for m in markers if m['id']==8],
            marker10_position_in_frame_ms=(onset['start']-f['offset'])/24,
            gaps_over_100ms=[dict(at_s=(b['at']-start)/1e9,gap_ms=(b['at']-a['at'])/1e6) for a,b in zip(frames,frames[1:]) if b['at']-a['at']>100_000_000])
        m7 = next(m for m in markers if m['id']==7)
        m9 = next(m for m in markers if m['id']==9)
        data['marker7_to9_excess_ms'] = (m9['start']-m7['start'])/24-2000
        if name=='go':
            data['callback_to_consumer'] = timing([(f['delivered_at']-f['at'])/1e6 for f in frames]) if 'delivered_at' in frames[0] else None
        elif (path/'python-callbacks.json').exists():
            callbacks = json.loads((path/'python-callbacks.json').read_text())
            data['callback_to_consumer'] = callback_pairing(frames,callbacks)
            data['ffi_callback_onset_ms'] = (callbacks[idx]['at']-packets[1000]['at'])/1e6
            data['ffi_callback_max_gap_ms'] = max((b['at']-a['at'])/1e6 for a,b in zip(callbacks,callbacks[1:]))
        data['native_stats'] = native_stats(path/f'{name}.jsonl',start)
        result[name] = data
    result['publisher_heartbeat'] = heartbeat(path/'publisher-heartbeat.json')
    return result


def report(root):
    original = analyze(root)  # read-only; retains original scoring
    rows = [details(root/row['run'],row) for row in original['runs'] if row['scenario'] in ('continuous','dtx')]
    result = dict(root=str(root),original_gate_summary={k:v for k,v in original['summary'].items() if k in ('continuous','dtx')},runs=rows)
    host = root/'host-heartbeat.json'
    if host.exists():
        h = json.loads(host.read_text())
        result['host_heartbeat'] = timing([(at-due)/1e6 for due,at in h['ticks']])
        result['host_load_range'] = [min(x[1] for x in h['load']),max(x[1] for x in h['load'])]
        for row in rows:
            start=row['publisher_start_ns']
            row['host_heartbeat_window']=timing([(at-due)/1e6 for due,at in h['ticks'] if start<=at<start+30_000_000_000])
            metrics=[row['publisher_lateness']['max_ms'],row['publisher_stages']['write_rtp']['max_ms'],
                     row['publisher_heartbeat']['max_ms'],row['go']['heartbeat']['max_ms'],
                     row['python']['heartbeat']['max_ms'],row['host_heartbeat_window']['max_ms']]
            row['scheduling_within_20ms']=max(metrics)<=20
    return result


if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('directory',type=Path)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    result=report(args.directory)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result['original_gate_summary'],indent=2))
