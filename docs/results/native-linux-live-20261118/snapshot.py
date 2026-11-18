from pathlib import Path
import subprocess,shutil,json
out=Path('/private/tmp/native-linux-live-20261118')
for repo,dirname in [('/Users/el7cosmos/src/cavos/rtp-agent','rtp-source'),('/Users/el7cosmos/src/cavos/agent-worker-golang','worker-source')]:
 root=Path(repo);dest=out/dirname;dest.mkdir(exist_ok=True)
 files=subprocess.check_output(['git','-C',repo,'ls-files','-c','-o','--exclude-standard','-z']).decode().split('\0')
 for name in set(files):
  p=Path(name)
  if not name or any(x in p.parts for x in ['refs','.git','.tmp','docs','.agents','.codex']):continue
  if not (p.suffix in ['.go','.c','.h','.cc','.cpp','.s'] or p.name in ['go.mod','go.sum','hold_music.mp3','INSTRUCTIONS.MD']):continue
  src=root/p
  if src.is_file(): (dest/p).parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(src,dest/p)
# Fixtures live only in this filtered build context.
for src,name in [('/Users/el7cosmos/src/cavos/rtp-agent/docs/results/native-sustained-soak-20261118/helpers_test.go.txt','local_native_acceptance_test.go'),('/Users/el7cosmos/src/cavos/rtp-agent/docs/results/native-agent-events-20261118/evidence_test.go.txt','local_native_evidence_test.go'),('/Users/el7cosmos/src/cavos/rtp-agent/docs/results/native-caption-startup-20261118/participant_ready_soak_test.go.txt','local_native_soak_test.go')]:
 shutil.copyfile(src,out/'worker-source/app'/name)
