import subprocess,json,math,array
from pathlib import Path
root=Path('/private/tmp/native-linux-live-20261118');results=[]
coeff={hz:([math.cos(2*math.pi*hz*i/24000) for i in range(24000)],[math.sin(2*math.pi*hz*i/24000) for i in range(24000)]) for hz in [600,1000]}
for arch in ['arm64','amd64']:
 for run in ['hold','forced']:
  for p in (root/('results-'+arch)/run).glob('*/audio.mp4'):
   raw=subprocess.check_output(['ffmpeg','-v','error','-i',str(p),'-f','s16le','-ac','1','-ar','24000','pipe:1']);a=array.array('h',raw);own,alien=(600,1000) if p.parent.name=='0' else (1000,600)
   mins=[];bad=0
   for start in range(24000,len(a)-24000,24000):
    w=a[start:start+24000];amp={hz:2*math.hypot(sum(x*y for x,y in zip(w,c)),sum(x*y for x,y in zip(w,s)))/len(w) for hz,(c,s) in coeff.items()}
    mins.append(amp[own]);bad+=amp[alien]>500 and amp[alien]>amp[own]*3
   results.append({'arch':arch,'run':run,'room':p.parent.name,'seconds':len(a)/24000,'windows_checked':len(mins),'min_own_marker_amplitude':min(mins) if mins else 0,'alien_dominant_windows':bad})
print(json.dumps(results,indent=2));assert results and all(r['windows_checked']>0 and r['min_own_marker_amplitude']>500 and r['alien_dominant_windows']==0 for r in results)
