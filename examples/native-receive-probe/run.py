"""Local-only controlled experiment. Starts only owned processes; never paid services."""
import argparse
import json
import os
import signal
import socket
import subprocess
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent

def records(path):
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]

def wait_ready(proc, path):
    until = time.monotonic() + 20
    while time.monotonic() < until:
        if proc.poll() is not None:
            raise RuntimeError(f'{path.stem} exited with {proc.returncode}')
        if any(x.get('event') == 'ready' for x in records(path)):
            return
        time.sleep(.05)
    raise TimeoutError(f'{path.stem} did not connect')

def stop(proc):
    if proc.poll() is None:
        proc.send_signal(signal.SIGINT)
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
            raise RuntimeError('shutdown exceeded three seconds; process killed')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--repetitions', type=int, default=10)
    parser.add_argument('--scenarios', nargs='+', default=['continuous', 'dtx', 'loss'], choices=['continuous','dtx','loss'])
    parser.add_argument('--output', default='results/full')
    args = parser.parse_args()
    os.chdir(ROOT)
    out = Path(args.output).resolve()
    out.mkdir(parents=True, exist_ok=False)
    # Reject an occupied port rather than accidentally joining another server.
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', 18991))
    config = out / 'server.yaml'
    config.write_text('port: 18991\nbind_addresses: [127.0.0.1]\nrtc:\n  use_external_ip: false\n  tcp_port: 18992\n  port_range_start: 19000\n  port_range_end: 19100\nkeys:\n  devkey: local-native-receive-experiment-secret\n')
    env = dict(os.environ, LIVEKIT_LIB_PATH=str(ROOT / '.cache/native/liblivekit_ffi.dylib'))
    if os.uname().sysname == 'Linux':
        env['LIVEKIT_LIB_PATH'] = str(ROOT / '.cache/native/liblivekit_ffi.so')
    logs = []
    server_log = open(out / 'server.log', 'w'); logs.append(server_log)
    server = subprocess.Popen(['livekit-server', '--config', str(config)], stdout=server_log, stderr=subprocess.STDOUT, env=env)
    children = []
    try:
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError('local server exited')
            try:
                with socket.create_connection(('127.0.0.1',18991),timeout=.2):
                    break
            except OSError:
                time.sleep(.1)
        else:
            raise TimeoutError('local server not listening')
        for scenario in args.scenarios:
            for rep in range(args.repetitions):
                dest = out / f'{scenario}-{rep:02}'
                dest.mkdir()
                room = f'probe-{scenario}-{rep}-{os.getpid()}'
                jwt = subprocess.check_output(['./probe','-mode','token','-room',room], env=env, text=True).strip()
                run_env = dict(env, PROBE_TOKEN=jwt)
                children = []
                for name, command in [
                    ('go',['./probe','-room',room,'-out',str(dest)]),
                    ('python',['.venv/bin/python','reference.py','--out',str(dest)])]:
                    log = open(dest / f'{name}.log','w'); logs.append(log)
                    proc = subprocess.Popen(command, env=run_env, stdout=log, stderr=subprocess.STDOUT)
                    children.append(proc)
                    wait_ready(proc,dest / f'{name}.jsonl')
                log = open(dest / 'publisher.log','w');logs.append(log)
                pub = subprocess.Popen(['./probe','-mode','publish','-room',room,'-scenario',scenario,'-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
                children.append(pub)
                code = pub.wait(timeout=45)
                if code:
                    raise RuntimeError(f'publisher failed: {code}')
                for proc in children[:2]:
                    stop(proc)
                    if proc.returncode:
                        raise RuntimeError(f'receiver failed: {proc.returncode}')
                children = []
                print(f'{scenario} {rep+1}/{args.repetitions} captured',flush=True)
    finally:
        for proc in children:
            stop(proc)
        stop(server)
        for log in logs:
            log.close()

if __name__ == '__main__':
    main()
