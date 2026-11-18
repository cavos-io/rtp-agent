import os,sys,subprocess
from pathlib import Path
arch=sys.argv[1];root=Path('/private/tmp/native-linux-live-20261118');env=os.environ.copy()
if not env.get('GH_TOKEN'):env['GH_TOKEN']=subprocess.check_output(['gh','auth','token'],text=True).strip()
args=['docker','buildx','build','--platform','linux/'+arch,'--progress','plain','--secret','id=gl_token,env=GH_TOKEN','--build-context','rtp-source='+str(root/'rtp-source'),'--build-arg','ENABLE_NATIVE=true','--build-arg','RUNTIME_IMAGE=agent-worker-validation:'+arch+'-native-true','-f',str(root/'Dockerfile'),'-t','native-linux-live:'+arch,'--load',str(root/'worker-source')]
with open(root/('build-'+arch+'.log'),'w') as log:r=subprocess.run(args,env=env,stdout=log,stderr=subprocess.STDOUT)
sys.exit(r.returncode)
