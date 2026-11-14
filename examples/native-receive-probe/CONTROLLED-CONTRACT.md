# Controlled content/latency follow-up

Frozen before new live runs. Preserve `results/full`, SUMMARY.json, MEASUREMENTS.csv,
ACCEPTANCE.md and all original failure classifications. No production changes.
Use the existing FFI 0.12.80 / Python RTC 1.1.21 pins, 24kHz mono / 50ms frames.

1. Audit original marker split and latency outlier against publisher scheduling
   and observer arrival records. Validate marker analysis on the exact generated
   source waveform. Keep original detector thresholds unchanged.
2. Instrument publisher wake/encode/WriteRTP stages, Go process heartbeat,
   Python event-loop heartbeat and FFI-queue callback timestamps, plus an
   independent runner heartbeat. Buffer heartbeats, Python callback traces and source PCM until exit;
   packet-stage records share the existing publisher JSON log.
   Callback timestamps in Python follow GIL acquisition/protobuf parsing; they
   are not an observation before GIL acquisition. No native SDK modifications.
3. Run ten continuous-silence and ten DTX baseline trials sequentially against
   one owned loopback server, one paired Go/Python receiver per publisher.
   Record versions, hashes, load and actual scheduler gaps. No privileged
   scheduling changes or unrelated process termination. This is a controlled
   fixture on a shared development host, not a dedicated real-time machine.
4. Separately inject a 450ms publisher-loop sleep before packet 402 (DTX) and
   packet 566 (continuous silence), matching original disturbance locations.
   Run three trials each. Preserve existing absolute-deadline catch-up behavior
   to test its effect; never implement a receive-side timing policy.

Original acceptance gates remain unchanged, including Go onset p95 <= Python
p95 +50ms and exactly one ordered detection of each marker 0..19. Report all
runs, without replacing failed baselines. Baseline attribution is strongest
when publisher lateness, WriteRTP duration and 10ms heartbeat overshoot each
stay <=20ms; larger excursions are explicitly scheduling-contaminated, not
silently excluded. Count input/output samples and inspect actual marker waveform
segments; frame counts alone cannot establish content identity.

Separate: sender schedule -> local WriteRTP -> independent observer arrival;
Go callback -> consumer; Python FFI queue callback -> iterator. All use host
CLOCK_MONOTONIC. Inspect paired per-run onset differences as a diagnostic alongside
unchanged difference-of-p95 gates. A 50ms frame can quantize marker delivery;
do not retroactively relax thresholds. Without direct native RTP/NetEq telemetry,
independent subscription differences cannot establish an internal native defect.

Stop after evidence distinguishes a harness artifact, reproduced disturbance,
or a concrete observability limit. Quiet reruns alone cannot establish why an
old failure occurred. No STT, LLM, TTS, custom playout or production rollout.

Before fault-injection results: the pinned FFI exposes track GetStats including
jitter-buffer target/delay, concealed samples, and acceleration/deceleration
counts. Add opt-in one-second sampling for the injection phase, plus three
uninjected trials per scenario with that same instrumentation. Those diagnostic
trials supplement the ten-trial baseline; do not replace it or use three-trial
p95 values as equivalent confidence. No RTC stats timestamp is subtracted from
CLOCK_MONOTONIC: use locally stamped observation times and counter differences.
