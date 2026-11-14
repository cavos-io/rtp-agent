"""Read-only recovery measurements; no receive-engine or acceptance changes."""
import argparse
import json
from pathlib import Path
import numpy as np
from analyze import analyze, read
from arrival_analyze import stats_points, synchronized
from controlled_analyze import callback_pairing


def recovery(points, threshold, origin):
    post=[p for p in points if p['t']>=origin]
    begin=confirmed=last=None
    for p in post:
        if not p['valid'] or p['value']>threshold+1e-6:
            begin=last=None
            continue
        if last is None or not 0 < p['t']-last <= .15+1e-6:
            begin=p['t']
        last=p['t']
        if last-begin>=2-1e-6:
            confirmed=last
            break
    good=[p for p in post if p['valid']]
    return dict(threshold_ms=threshold,begin_s=begin-origin if confirmed is not None else None,
        confirmed_s=confirmed-origin if confirmed is not None else None,
        observation_end_s=post[-1]['t']-origin if post else None,
        peak_ms=max((p['value'] for p in good),default=None),
        later_excursions=sum(p['valid'] and p['value']>threshold+1e-6 and p['t']>confirmed for p in post) if confirmed is not None else None,
        invalid_intervals=sum(not p['valid'] for p in post))


def marker_recovery(markers, threshold, origin):
    post=[m for m in markers if m['id']>=10]
    for a,b,c in zip(post,post[1:],post[2:]):
        if [a['id'],b['id'],c['id']]==list(range(a['id'],a['id']+3)) and all(m['callback_ms']<=threshold for m in (a,b,c)):
            return dict(threshold_ms=threshold,first_marker=a['id'],observed_by_s=a['source_s']-origin,
                confirmed_at_marker_s=c['source_s']-origin,
                later_excursions=sum(m['callback_ms']>threshold for m in post if m['id']>c['id']))
    return dict(threshold_ms=threshold,first_marker=None,observed_by_s=None,confirmed_at_marker_s=None)


def report(root):
    original=analyze(root)
    output=[]
    for row in original['runs']:
        path=root/row['run'];pub=read(path/'publisher.jsonl')
        start=next(e['at'] for e in pub if e['event']=='start')
        stalls=[e for e in pub if e['event']=='injected_stall']
        if len(stalls)>1: raise ValueError('expected at most one stall')
        origin=(stalls[0]['end']-start)/1e9 if stalls else 11.770
        packets={p['index']:p for p in pub if p['event']=='packet'}
        events={s:read(path/(s+'.jsonl')) for s in ['go','python']}
        points={s:stats_points(events[s],s)[0] for s in events}
        paired=synchronized(points['go'],points['python'])
        result=dict(run=row['run'],stall_end_source_s=origin,controlled_stall=bool(stalls),original_checks=row['checks'])
        for side in events:
            timeline=[]
            for a,p in zip(paired,paired[1:]):
                q=p[side];previous=a[side];t=(q['capture']-start)/1e9
                if t>30:continue
                valid=(a['valid'] and p['valid'] and p['slot']==a['slot']+1 and
                    50_000_000<=q['capture']-previous['capture']<=150_000_000 and
                    'buffer_ms' in q and 'target_ms' in q)
                timeline.append(dict(t=t,valid=valid,buffer_ms=q.get('buffer_ms'),target_ms=q.get('target_ms')))
            side_result={}
            for metric in ['buffer_ms','target_ms']:
                pre=[p[metric] for p in timeline if 8<=p['t']<=11 and p['valid']]
                if len(pre)<10: raise ValueError('insufficient pre-stall native reference')
                reference=float(np.median(pre))
                values=[dict(t=p['t'],valid=p['valid'],value=p[metric]) for p in timeline]
                side_result[metric]=dict(reference_ms=reference,**recovery(values,reference+20,origin))
            frames=[e for e in events[side] if e['event']=='frame']
            callbacks=json.loads((path/'python-callbacks.json').read_text()) if side=='python' else frames
            if side=='python':callback_pairing(frames,callbacks)
            markers=[]
            for m in row[side]['markers']:
                index=next(i for i,f in enumerate(frames) if f['offset']+f['samples']>m['start'])
                source=packets[(m['id'] if m['id']<10 else m['id']+10)*50]['at']
                markers.append(dict(id=m['id'],source_s=(source-start)/1e9,callback_ms=(callbacks[index]['at']-source)/1e6,
                    original_delivery_ms=m['latency_ms'],position_in_frame_ms=(m['start']-frames[index]['offset'])/24))
            reference=float(np.percentile([m['callback_ms'] for m in markers if 2<=m['id']<=9],95))
            side_result['content']=dict(reference_p95_ms=reference,**marker_recovery(markers,reference+50,origin),markers=markers)
            # Coarse post-stall trajectory, retaining valid sample counts.
            side_result['buffer_trajectory']=[]
            for delay in [0,1,2,4,8,12,16]:
                values=[p['buffer_ms'] for p in timeline if p['valid'] and origin+delay<=p['t']<origin+delay+1]
                side_result['buffer_trajectory'].append(dict(after_stall_s=delay,valid_samples=len(values),median_ms=float(np.median(values)) if values else None))
            result[side]=side_result
        output.append(result)
    return dict(root=str(root),original_summary=original['summary'],runs=output)


if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('directory',type=Path);parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    with args.output.open('x') as out:
        json.dump(report(args.directory),out,indent=2);out.write('\n')
