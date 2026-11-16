# Python Agents versus Go: two-second signaling outage

Measured2026-11-16 on macOS27.2.1 ARM64. Python Agents also incurs a multi-second
PCM gap during native full restart. These observations do not establish a
language-specific latency advantage.

## Pinned environment and comparison boundary

- Python3.14.4, livekit-agents1.8.10, livekit RTC1.1.21.
- Native FFI0.12.80 / rust-sdks5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb.
- Both processes explicitly load the SAME liblivekit_ffi.dylib via
  LIVEKIT_LIB_PATH, SHA256
  41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735.
- Go1.27.4, caller server-sdk-go2.17.0, local LiveKit server1.13.11.
- Complete installed Python dependency pins: agents-outage-requirements.txt.
  Source/library/helper hashes are in results/python-outage-20261116/versions.json.

The reference uses the installed Agents _ParticipantAudioInputStream, the actual
RoomIO audio-input implementation, including its subscription replacement and
stream-close behavior. It is not a bare RTC AudioStream loop or mocked PCM source.
No AgentSession/provider conversation or inference runs. AGC and noise processing
are disabled in this media comparison; this is not proof of every default Python
AgentSession shutdown/configuration behavior. It uses private framework APIs and
is a pinned experiment, not a supported production integration.

Python is launched as a reference subprocess behind the existing Go measurement
harness. Both run the SAME publisher generator, input sample format24k mono50ms,
output tones, subscription barriers, native library and local two-second HTTP503
signaling proxy fault in separate rooms. RTP is direct. Python output uses RTC
AudioSource; Agents owns input rebinding. No custom jitter/padding/pacing added.

## Measurements

Each completed trial observes10s of the publisher. Callback gaps and wall/PCM
measurements use the SAME parent Go monotonic clock for both implementations;
Python event timestamps are supplementary and are not subtracted from Go clocks.

| Trial | New signal handshake | Max PCM callback gap | PCM received | Framework-added silence | Outcome |
|---|---:|---:|---:|---:|---|
| Python03 | >5s | not completed | not completed | present | Signaling deadline failed |
| Python04 | 3.200s | 3.046s | 7.500s | 0.500s | Recovered;3s gap gate failed |
| Python05 | 2.546s | 2.544s | 7.950s | 0.500s | Passed recovery gates |
| Go01 | 2.979s | 2.768s | 7.300s | 0 | Passed recovery gates |
| Go02 | 2.989s | 2.987s | 7.050s | 0 | Passed recovery gates |

Completed trials all resumed nonzero input content in the final three1s windows,
received the post-reconnect output marker (49/50 Python,50/50 Go decoded frames),
and ended with helper exit0, reap, no forced kill and completed cleanup. Native
retry uses exponential backoff with full jitter; these small trial counts do not
isolate a Go/Python performance difference. A101 handshake is not acoustic
recovery, and frame-energy/marker presence is not sample-perfect speech parity.

The pre-existing5s signaling bound,3s full-restart gap/deficit bounds and250ms
steady-window bounds were retained. The original strict uninterrupted-PCM test
also remains available; a full restart does not pass it. A Python deadline failure
and Python04's gap failure are retained, not excluded as successful trials.

## Important framework difference

Installed Agents _ParticipantAudioInputStream._forward_task sends
silence_frame(0.5, sample_rate, num_channels) after an old stream ends while
attached. Both completed Python trials logged one12000-sample, all-zero frame
at this boundary. Consequently Python's7.500/7.950s totals contain0.500s of
framework-generated silence; excluding that frame leaves7.000/7.450s, respectively.
This is source-backed and observed framework behavior, NOT uninterrupted native
media reception. It does not fill the full reconnect gap.

The first two Python trials failed because this500ms frame exceeded the Go
helper IPC line/frame limits. The reference wrapper now fragments existing frames
into50ms IPC chunks, without altering sample count/content or pacing delivery.
Unit tests verify byte identity, no padding and rejection of unsupported shapes.
python-01.log and python-02.log remain as harness failures, not receive-engine
results. Python03 additionally logged an unknown-handle warning during its early
abort cleanup; completed Python04/05 did not. Python cleanup calls the SDK close
methods and explicit FFI disposal; this is not a native allocation/leak audit.

## Preservation and reproduction

Artifacts and comparison.json: results/python-outage-20261116/.
All3207 earlier artifact files verified unchanged. No refs/ or production transport
changes. Dependencies installed only into .cache/agents-venv, not the system or
existing reference environment. No cloud or paid inference calls were made.

From the repository root, recreate the isolated environment and wrapper:

```sh
uv venv --python 3.14.4 examples/native-receive-probe/.cache/agents-venv
uv pip sync --python examples/native-receive-probe/.cache/agents-venv/bin/python \
  examples/native-receive-probe/agents-outage-requirements.txt
cat > examples/native-receive-probe/.cache/python-agents-helper <<EOF_HELPER
#!/bin/sh
exec "$PWD/examples/native-receive-probe/.cache/agents-venv/bin/python" "$PWD/examples/native-receive-probe/agents_outage_helper.py" "\$@"
EOF_HELPER
chmod +x examples/native-receive-probe/.cache/python-agents-helper
```

Start a local server with the preserved server.yaml, then use a fresh results
path for EACH invocation:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/python-outage-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/python-agents-helper" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestNativePrimarySignalOutageRecovery$' -count=1 -timeout 90s -v
```

For Go, build the probe with -race and select .cache/probe-reconnect as the helper,
with another fresh result directory. A test failure still produces useful
measurement artifacts when recovery completes. Do not overwrite failed trials.

Validation: framing unit tests and tagged starter vet passed. Existing unrelated
repository race/parity-wrapper failures remain separate; no full-suite pass or
bit-identical framework parity is claimed.

## Checkpoint acceptance

The observed reconnect gap is accepted for now on the experimental opt-in path.
This acceptance retains all failed measurements and does not relax the existing
integration gates or promote native transport to default. Raw logs/recordings
remain locally preserved under ignored results/; this report and the pinned
reference harness are versioned. Earlier unrelated failures remain documented.
