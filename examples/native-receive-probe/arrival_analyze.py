"""Socket-boundary RTP timing and synchronized native stats. Read-only input."""
import argparse
import json
from pathlib import Path
import numpy as np
from analyze import analyze, read
from controlled_analyze import callback_pairing


def align_packets(packets, trace, ssrc):
    sent=[p for p in packets if p['sent']]
    media=[p for p in trace if p.get('ssrc')==ssrc]
    by_header={}
    for p in media:
        by_header.setdefault((p['seq'],p['ts']),[]).append(p)
    candidates=[]
    first=sent[0]
    # Find a fixed SFU header offset; require every transmitted media header.
    for p in media:
        if not first['at']<=p['at']<first['at']+1_000_000_000:
            continue
        seq_offset=(p['seq']-first['seq'])%65536
        ts_offset=(p['ts']-first['ts'])%2**32
        keys=[((s['seq']+seq_offset)%65536,(s['ts']+ts_offset)%2**32) for s in sent]
        if all(k in by_header for k in keys):
            candidates.append((seq_offset,ts_offset,keys))
    candidates=list({(a,b):keys for a,b,keys in candidates}.items())
    if len(candidates)!=1:
        raise ValueError(f'complete unique media mapping required: {len(candidates)} candidates')
    (seq_offset,ts_offset),keys=candidates[0]
    duplicates=sum(len(by_header[k])-1 for k in keys)
    if duplicates:
        raise ValueError(f'duplicate media headers in socket trace: {duplicates}')
    ordered=[by_header[k][0] for k in keys]
    if any(p['at']<s['at'] for s,p in zip(sent,ordered)):
        raise ValueError('socket receipt predates publisher on shared clock')
    return {s['index']:p for s,p in zip(sent,ordered)},dict(ssrc=ssrc,media_packets=len(keys),extra_packets=len(media)-len(keys),seq_offset=seq_offset,ts_offset=ts_offset)


def stats_points(events, side):
    anchors=[e for e in events if e['event']=='clock_anchor']
    if len(anchors)!=2:
        raise ValueError('two clock anchors required')
    offsets=[a['realtime']-(a['before']+a['after'])//2 for a in anchors]
    drift=abs(offsets[1]-offsets[0])
    if drift>1_000_000:
        raise ValueError('realtime/monotonic clock offset moved >1ms')
    requests={e['async_id']:e for e in events if e['event']=='native_stats_request'}
    points=[]
    for e in events:
        if e['event']!='native_stats':
            continue
        inbound=[s['inbound_rtp'] for s in e['stats'].get('stats',[]) if 'inbound_rtp' in s and s['inbound_rtp']['stream']['kind']=='audio']
        if not inbound:
            continue
        if len(inbound)!=1:
            raise ValueError('expected one inbound audio stream')
        r=inbound[0];b=r['inbound']
        req=requests[int(e['stats']['async_id'])] if side=='go' else e
        requested=req['at'] if side=='go' else req['requested_at']
        capture=int(r['rtc']['timestamp'])*1000-offsets[0]
        p=dict(slot=req['slot'],scheduled=req['scheduled'],requested=requested,response=e['at'],capture=capture,
            response_ms=(e['at']-requested)/1e6,capture_age_ms=(e['at']-capture)/1e6,
            ssrc=r['stream']['ssrc'],packets=int(r['received']['packets_received']),lost=int(r['received']['packets_lost']),
            discarded=int(b['packets_discarded']),emitted=int(b['jitter_buffer_emitted_count']),
            delay=float(b['jitter_buffer_delay']),target=float(b['jitter_buffer_target_delay']),
            concealed=int(b['concealed_samples']),inserted=int(b['inserted_samples_for_deceleration']),
            removed=int(b['removed_samples_for_acceleration']))
        if p['capture_age_ms'] < -1:
            raise ValueError('native timestamp calibration inconsistent with response')
        if points:
            previous=points[-1];emitted=p['emitted']-previous['emitted']
            if emitted>0:
                p['buffer_ms']=1000*(p['delay']-previous['delay'])/emitted
                p['target_ms']=1000*(p['target']-previous['target'])/emitted
            p['concealed_delta']=p['concealed']-previous['concealed']
            p['removed_delta']=p['removed']-previous['removed']
            p['inserted_delta']=p['inserted']-previous['inserted']
        points.append(p)
    if len(points)<2 or len({p['ssrc'] for p in points})!=1:
        raise ValueError('insufficient or multiple-stream native stats')
    return points,dict(offset_drift_ns=drift,max_anchor_bracket_ns=max(a['after']-a['before'] for a in anchors),max_response_ms=max(p['response_ms'] for p in points),max_capture_age_ms=max(p['capture_age_ms'] for p in points))


def synchronized(go,python):
    by_slot={p['slot']:p for p in python}
    pairs=[]
    for g in go:
        p=by_slot.get(g['slot'])
        if p is None:continue
        if g['scheduled']!=p['scheduled']:
            raise ValueError('stats grid differs between receivers')
        skew=abs(g['requested']-p['requested'])/1e6
        producer_skew=abs(g['capture']-p['capture'])/1e6
        valid=skew<=10 and producer_skew<=20 and 0<=g['response_ms']<=20 and 0<=p['response_ms']<=20
        pairs.append(dict(slot=g['slot'],at=(g['capture']+p['capture'])//2,request_skew_ms=skew,
            producer_skew_ms=producer_skew,valid=valid,go=g,python=p))
    return pairs


def distribution(values):
    return dict(min=float(min(values)),median=float(np.median(values)),p95=float(np.percentile(values,95)),max=float(max(values)))


def detail(path,row,traced=True):
    pub=read(path/'publisher.jsonl');packets=[p for p in pub if p['event']=='packet']
    start=next(p['at'] for p in pub if p['event']=='start')
    result=dict(run=path.name,receiver_order=json.loads((path/'run-config.json').read_text())['receiver_order'],
        go_onset_ms=row['go']['second_onset_ms'],python_onset_ms=row['python']['second_onset_ms'],
        onset_difference_ms=row['go']['second_onset_ms']-row['python']['second_onset_ms'],original_checks=row['checks'])
    streams={};maps={};points={}
    for side in ['go','python']:
        events=read(path/(side+'.jsonl'))
        points[side],clock=stats_points(events,side)
        frames=[e for e in events if e['event']=='frame'];streams[side]=frames
        marker=next(m for m in row[side]['markers'] if m['id']==10)
        idx=next(i for i,f in enumerate(frames) if f['offset']+f['samples']>marker['start'])
        data=dict(clock=clock,marker_position_ms=(marker['start']-frames[idx]['offset'])/24,
            native_lost_final=points[side][-1]['lost'],native_discarded_final=points[side][-1]['discarded'])
        if side=='python':
            callbacks=json.loads((path/'python-callbacks.json').read_text())
            data['callback_to_consumer']=callback_pairing(frames,callbacks)
            data['callback_onset_ms']=(callbacks[idx]['at']-packets[1000]['at'])/1e6
        else:
            data['callback_onset_ms']=row[side]['second_onset_ms']
            data['consumer_onset_ms']=(frames[idx]['delivered_at']-packets[1000]['at'])/1e6
            data['callback_to_consumer']=dict(p95_ms=row[side]['callback_to_consumer_p95_ms'],max_ms=row[side]['callback_to_consumer_max_ms'])
        if traced:
            trace=read(path/(side+'-arrivals.jsonl'))
            if trace[0]['dropped'] or trace[0]['packets']!=len(trace)-1:
                raise ValueError('dropped or incomplete socket trace')
            maps[side],data['mapping']=align_packets(packets,trace[1:],points[side][0]['ssrc'])
            data['socket_transit_ms']=distribution([(p['at']-packets[i]['at'])/1e6 for i,p in maps[side].items()])
            data['marker_packet_transit_ms']=(maps[side][1000]['at']-packets[1000]['at'])/1e6
            data['callback_after_socket_ms']=data['callback_onset_ms']-data['marker_packet_transit_ms']
        result[side]=data
    pairs=synchronized(points['go'],points['python'])
    if not pairs:raise ValueError('no paired stats slots')
    valid=[p for p in pairs if p['valid']]
    result['sync']=dict(pairs=len(pairs),valid=len(valid),max_request_skew_ms=max(p['request_skew_ms'] for p in pairs),max_producer_skew_ms=max(p['producer_skew_ms'] for p in pairs))
    # Compare only adjacent valid slots, so intervals cover approximately the same window.
    near=[p for a,p in zip(pairs,pairs[1:]) if a['valid'] and p['valid'] and p['slot']==a['slot']+1 and
          abs(p['at']-(start+20_000_000_000))<=150_000_000 and 'buffer_ms' in p['go'] and 'buffer_ms' in p['python']]
    if not near:raise ValueError('no synchronized adjacent native samples near second onset')
    chosen=min(near,key=lambda p:abs(p['at']-(start+20_000_000_000)))
    result['near_onset_stats']=dict(at_s=(chosen['at']-start)/1e9,request_skew_ms=chosen['request_skew_ms'],producer_skew_ms=chosen['producer_skew_ms'],
        go_buffer_ms=chosen['go']['buffer_ms'],python_buffer_ms=chosen['python']['buffer_ms'],
        go=chosen['go'],python=chosen['python'])
    # Illustrative frame-boundary estimate, not a new acceptance metric or a
    # measured per-sample native timestamp. Detector localization is 10ms.
    result['frame_position_adjusted_callback_difference_estimate_ms']=(
        result['go']['callback_onset_ms']-result['python']['callback_onset_ms']+
        result['go']['marker_position_ms']-result['python']['marker_position_ms'])
    result['native_timeline']=[dict(at_s=(p['at']-start)/1e9,**{k:v for k,v in p.items() if k!='at'}) for p in pairs]
    if traced:
        skew=[(maps['go'][i]['at']-maps['python'][i]['at'])/1e6 for i in maps['go']]
        result['arrival_skew_ms']=distribution(skew)
        result['arrival_abs_skew_ms']=distribution([abs(x) for x in skew])
        result['marker_arrival_skew_ms']=(maps['go'][1000]['at']-maps['python'][1000]['at'])/1e6
        result['callback_difference_ms']=result['go']['callback_onset_ms']-result['python']['callback_onset_ms']
        result['post_socket_difference_ms']=result['go']['callback_after_socket_ms']-result['python']['callback_after_socket_ms']
        result['largest_arrival_skews']=[dict(index=i,skew_ms=(maps['go'][i]['at']-maps['python'][i]['at'])/1e6) for i in sorted(maps['go'],key=lambda i:abs(maps['go'][i]['at']-maps['python'][i]['at']),reverse=True)[:5]]
    return result


def report(root):
    manifest=json.loads((root/'run-manifest.json').read_text())
    original=analyze(root)
    rows=[]
    for row in original['runs']:
        try:
            rows.append(detail(root/row['run'],row,manifest['arguments']['packet_trace']))
        except (ValueError,KeyError,StopIteration) as exc:
            # Preserve the failed full-trace attribution. Stats may still be
            # interpretable even when packets were absent before subscription.
            failed=dict(run=row['run'],attribution_error=str(exc),original_checks=row['checks'],go_onset_ms=row['go']['second_onset_ms'],python_onset_ms=row['python']['second_onset_ms'])
            try:
                failed.update(detail(root/row['run'],row,False))
            except (ValueError,KeyError,StopIteration) as stats_error:
                failed['stats_attribution_error']=str(stats_error)
            rows.append(failed)
    return dict(root=str(root),original_summary=original['summary'],runs=rows)


if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('directory',type=Path);parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args();data=report(args.directory);args.output.write_text(json.dumps(data,indent=2)+'\n')
    for r in data['runs']:
        print(json.dumps({k:v for k,v in r.items() if k not in ['native_timeline','go','python','near_onset_stats']}))
