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

def stop_all(processes):
    failures = []
    for proc in processes:
        try:
            stop(proc)
        except Exception as exc:
            failures.append(exc)
    if failures:
        raise ExceptionGroup('process cleanup failed', failures)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--diagnostics', action='store_true')
    parser.add_argument('--native-stats', action='store_true')
    parser.add_argument('--stall-packet', type=int, default=-1)
    parser.add_argument('--repetitions', type=int, default=10)
    parser.add_argument('--binary', default='./probe')
    parser.add_argument('--scenarios', nargs='+', default=['continuous', 'dtx', 'loss'], choices=['continuous','dtx','loss'])
    parser.add_argument('--output', default='results/full')
    args = parser.parse_args()
    if args.repetitions < 1 or not -1 <= args.stall_packet < 1500:
        parser.error('positive repetitions and stall-packet in -1..1499 required')
    if (args.native_stats or args.stall_packet != -1) and not args.diagnostics:
        parser.error('stats and fault injection require --diagnostics')
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
    env['PROBE_DIAGNOSTICS'] = '1' if args.diagnostics else '0'
    env['PROBE_NATIVE_STATS'] = '1' if args.native_stats else '0'
    env['PROBE_STALL_PACKET'] = str(args.stall_packet)
    if args.diagnostics:
        import hashlib, platform
        from diagnostics import monitor
        (out / 'run-manifest.json').write_text(json.dumps(dict(arguments=vars(args),
            platform=platform.platform(), load=os.getloadavg(),
            hashes={str(p):hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in
                [args.binary, env['LIVEKIT_LIB_PATH'], '.cache/speech.pcm', 'reference.py', 'run.py', 'diagnostics.py', 'analyze.py']}
        ), indent=2))
    finish_monitor = None
    logs = []
    server_log = open(out / 'server.log', 'w'); logs.append(server_log)
    server = subprocess.Popen(['livekit-server', '--config', str(config)], stdout=server_log, stderr=subprocess.STDOUT, env=env)
    children = []
    try:
        if args.diagnostics:
            finish_monitor = monitor(out / 'host-heartbeat.json')
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
                jwt = subprocess.check_output([args.binary,'-mode','token','-room',room], env=env, text=True).strip()
                run_env = dict(env, PROBE_TOKEN=jwt)
                children = []
                for name, command in [
                    ('go',[args.binary,'-room',room,'-out',str(dest)]),
                    ('python',['.venv/bin/python','reference.py','--out',str(dest)])]:
                    log = open(dest / f'{name}.log','w'); logs.append(log)
                    proc = subprocess.Popen(command, env=run_env, stdout=log, stderr=subprocess.STDOUT)
                    children.append(proc)
                    wait_ready(proc,dest / f'{name}.jsonl')
                log = open(dest / 'publisher.log','w');logs.append(log)
                pub = subprocess.Popen([args.binary,'-mode','publish','-room',room,'-scenario',scenario,'-out',str(dest)],env=env,stdout=log,stderr=subprocess.STDOUT)
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
        try:
            stop_all([*reversed(children), server])
        finally:
            for log in logs:
                log.close()
            if finish_monitor is not None:
                finish_monitor()

if __name__ == '__main__':
    main()
