"""Generate unmodified pinned upstream protobuf contracts; generated code is checked in for ordinary Go builds."""
from pathlib import Path
import subprocess
root = Path(__file__).resolve().parent
src = root / '.cache/rust-sdks-5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb/livekit-ffi/protocol'
out = root.parent.parent / 'interface/worker/livekit/ffipb'
out.mkdir(parents=True, exist_ok=True)
files = sorted(src.glob('*.proto'))
subprocess.run(['protoc', f'-I{src}', f'--go_out={out}', '--go_opt=paths=source_relative',
    *[f'--go_opt=M{f.name}=github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb' for f in files],
    *map(str, files)], check=True)
