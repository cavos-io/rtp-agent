import json,re,pathlib,statistics,sys
root=pathlib.Path(sys.argv[1] if len(sys.argv)>1 else '/private/tmp/native-soak-20261118')
results={}
for run in ['full','pion-full','race-smoke','ready-repeat']:
 rows=[json.loads(s) for s in (root/run/'resources.jsonl').read_text().splitlines()]
 active=[s for s in rows if s['phase']=='active--1'];steady=[s for s in active if s['seconds']>=120]
 clean=[s for s in rows if s['phase'].startswith('clean-') and s['phase']!='clean--1']
 def stats(ss):
  if not ss:return None
  xs=[s['seconds'] for s in ss];ys=[s['rss_kib']/1024 for s in ss]
  mx,my=statistics.mean(xs),statistics.mean(ys)
  den=sum((x-mx)**2 for x in xs)
  return dict(samples=len(ss),rss_mib_min=min(ys),rss_mib_max=max(ys),rss_mib_first=ys[0],rss_mib_last=ys[-1],rss_mib_per_minute=(sum((x-mx)*(y-my) for x,y in zip(xs,ys))/den*60 if den else None),heap_mib_first=ss[0]['heap_bytes']/1048576,heap_mib_last=ss[-1]['heap_bytes']/1048576,goroutines_min=min(s['goroutines'] for s in ss),goroutines_max=max(s['goroutines'] for s in ss))
 peers=[]; failures=[]; readiness=[]
 for p in sorted((root/run).glob('*.peer.log')):
  text=p.read_text()
  if '--- FAIL:' in text: failures.append({'file':p.name,'assertions':[line.strip() for line in text.splitlines() if 'local_native_soak_test.go:' in line]})
  readiness += [{'file':p.name,'line':line.strip()} for line in text.splitlines() if 'READY participant_known=' in line]
  m=re.search(r'PEER room=(\S+) sent=(\d+) markers=(\d+) wrong=(\d+) turns=(\d+)',text)
  if m:peers.append(dict(zip(['room','sent','markers','wrong','turns'],[m[1],*[int(m[i]) for i in range(2,6)]])))
 log=(root/(run+'.log')).read_text();calls=[]
 for m in re.finditer(r'CALL room=(\S+) frames=(\d+) wrong=(\d+) replies=(\d+) cleanup=(\{[^\n]+\})',log):
  calls.append(dict(room=m[1],frames=int(m[2]),wrong=int(m[3]),replies=int(m[4]),cleanup=json.loads(m[5])))
 results[run]=dict(active=stats(active),steady=stats(steady),clean=stats(clean),peers=peers,calls=calls,peer_failures=failures,readiness=readiness,passed='--- PASS: TestLocalNativeSoak' in log,race_detected='WARNING: DATA RACE' in log)
print(json.dumps(results,indent=2))
