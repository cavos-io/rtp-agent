import sys,subprocess,pathlib,json,os
arch,case=sys.argv[1:];r=pathlib.Path('/private/tmp/native-linux-live-20261118');out=r/('results-'+arch);out.mkdir(exist_ok=True);out.chmod(0o777)
name='linux-'+arch+'-'+case
args=['docker','run','--rm','--name',name,'--platform','linux/'+arch,'--network','container:native-linux-live-server','--mount','type=bind,src='+str(r/'ps')+',dst=/usr/local/bin/ps,readonly','--mount','type=bind,src='+str(out)+',dst=/results','-w','/tmp','-e','LIVEKIT_LIB_PATH=/opt/livekit/liblivekit_ffi.so','-e','NATIVE_ACCEPTANCE_URL=ws://127.0.0.1:17880','-e','NATIVE_ACCEPTANCE_OUT=/results/'+case,'-e','NATIVE_TEST_INPROCESS=1','-e','NATIVE_TEST_URL=ws://127.0.0.1:17880','-e','NATIVE_ROOM_RESULT_DIR=/results/'+case]
if case=='repeat':args+=['-e','SOAK_REPEAT_ONLY=1'];test='TestLocalNativeSoak'
elif case in ['hold','forced','drain']:
 if case=='hold':args+=['-e','NATIVE_ACCEPTANCE_HOLD=1']
 if case=='forced':args+=['-e','NATIVE_ACCEPTANCE_FORCE=1']
 test='TestLocalNativeActiveDrainAcceptance'
else:
 args+=['--entrypoint','/validation/starter.test']
 test={'captions':'TestStarterInProcessNativeCaptions','lifecycle':'TestStarterInProcessNativeAudioLifecycle','duplex':'TestStarterInProcessNative','dtmf':'TestStarterInProcessNativeDTMF','state':'TestStarterInProcessNativeClientState','reconnect':'TestNativePrimarySignalOutageRecovery'}[case]
args+=['native-linux-live:'+arch,'-test.run=^'+test+'$','-test.v','-test.timeout=5m']
with open(r/(arch+'-'+case+'.log'),'w') as log:p=subprocess.run(args,stdout=log,stderr=subprocess.STDOUT)
(r/(arch+'-'+case+'-exit.json')).write_text(json.dumps({'exit_code':p.returncode,'command':args},indent=2)+'\n');print(arch,case,p.returncode);sys.exit(p.returncode)
