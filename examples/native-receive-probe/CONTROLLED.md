# Controlled content and latency investigation

**A delayed publisher plus its existing catch-up burst reproduces the content
failure and native buffer buildup. The original Go/Python relative-latency
outlier was not reproduced.** There is no demonstrated Go wrapper defect.
Original failures remain failures; this investigation changes no acceptance
threshold, marker detector, native playout policy, or production code.

## Measurements

Pinned macOS ARM64, Go 1.27.4, Python RTC 1.1.21, LiveKit FFI 0.12.80,
Rust SDK `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, local server 1.13.11.
Same publisher, separate native Go/Python subscriptions, 24kHz mono / 50ms PCM.
All timestamps used for latency share host `CLOCK_MONOTONIC`; RTP uses 48kHz.

| Dataset | Trials | Content gate, Go and Python | Second onset p95, Go / Python | Original relative-latency gate |
|---|---:|---:|---:|---|
| Continuous-silence baseline | 10 | 10/10 | 82.27 / 78.86ms | Pass |
| DTX baseline | 10 | 10/10 | 49.82 / 49.78ms | Pass |
| Continuous silence, stats enabled, no injection | 3 | 3/3 | 75.64 / 42.40ms | Pass |
| DTX, stats enabled, no injection | 3 | 3/3 | 28.66 / 30.64ms | Pass |
| DTX, 450ms publisher stall at packet 402 | 3 | **0/3**, marker 8 split | 48.25 / 49.79ms | Pass |
| Continuous silence, 450ms stall at packet 566 | 3 | 3/3 | 138.59 / 148.78ms | Pass |

Every run passed the other original per-run checks: scenario packet evidence,
sustained PCM, shutdown and wrapper errors. Go produced 29.000–29.050s and
Python 29.000s in the common 29s window. Three-trial p95 figures are descriptive,
not equivalent confidence to the ten-trial baseline. Exact summaries, binary
hashes and per-phase manifests are in [CONTROLLED-SUMMARY.json](CONTROLLED-SUMMARY.json).

## Content: fragmentation and concealment, not proof of duplicate speech

The preserved `dtx-03` has a **453.805ms late publisher packet 402** at source
8.04s. Packet 401 arrived normally; delayed packets then arrived in a burst.
The observer's corresponding media gap is 473.34ms. After normalizing the SFU's
constant sequence/timestamp offsets, all 1,032 transmitted media packets align
in order and payload size. Its 34 prefix and two suffix packets are outside
that media sequence; they must not be treated as extra published speech.

The original PCM contains two marker-8 regions, **150ms and 220ms**, separated
by **310ms**. Marker 7→9 spacing expands by **440ms**. A single source marker is
250ms. The existing detector reports two IDs whenever one tone is fragmented
by a sufficiently long gap; a deterministic analyzer test demonstrates that
ambiguity. This does not make the audible disruption disappear or turn the
original content gate into a pass. It also does not establish replay of a
complete marker or duplicate speech.

Sleeping only the publisher loop for 450ms before packet 402 reproduced the
split marker in **all three live trials, in both receivers**. The first trial
matches the original 150ms/310ms/220ms geometry exactly. Captured encoder-input
PCM has exactly one ordered instance of each marker; all 32 new source files
have the same digest. This source check and the synthetic detector test are
separate from the real receive-engine measurements.

Direct native track statistics show concealment increasing around the injected
gap (20,760 samples in each receiver in the first trial), followed by roughly
460ms of buffer residence. Native sampled packet-loss and discard counters end
at zero. Go callback→consumer p95 stays below 0.5ms and Python FFI-queue→iterator
p95 below 0.9ms in these DTX injections. The original 476ms Python delivery gap
is therefore **not necessary** to produce the content disruption. The native
engine's concealment/buffering response to late input is directly observed;
its precise internal algorithm is not inferred from PCM alone.

## Latency: native buffering reproduced; original differential remains open

Preserved `continuous-02` has a **466.283ms publisher stall at packet 566**, during
silence at 11.32s, and a 487.495ms observer media gap. Second-speech onset later
measures **200.990ms Go / 108.721ms Python**. Both detected marker onsets occupy
the same 30ms position within their application frames, so that 92.269ms
per-run difference cannot simply be dismissed as a different frame position.
The original aggregate p95 difference remains **50.32ms against a 50ms gate**.

The matched publisher-only injection reproduces substantial buffer buildup in
both native receivers: peak interval-average jitter-buffer residence is
455–467ms, with roughly **86–124ms still present near second-speech onset**.
Uninjected stats controls have approximately 30–36ms near that onset. Native
acceleration counters increase as the buffer drains. These are differences of
native cumulative counters divided by emitted samples; native RTC timestamps
are never subtracted from the host clock.

The three injected continuous-silence trials give 137.88–138.62ms Go and
137.65–149.49ms Python onset. Wrapper delay p95 remains below 1ms. Thus the
extra absolute latency is inside native receive/playout, triggered by the
publisher disturbance, rather than accumulating in the instrumented wrapper
queues. The **original Go-over-Python outlier remains unreproduced**; its
per-instance native stats and callback/consumer separation were not recorded.
Similar durations or clean reruns cannot establish its exact cause.

## Attribution limits and host conditions

The original simultaneous publisher and Python pauses are consistent with a
shared host scheduling or blocking-I/O disturbance. Old telemetry cannot
separate timer wake delay, codec execution, WriteRTP, or synchronous logging.
The new deliberate publisher sleep proves a sufficient trigger; it does not
prove what caused the original publisher to stall.

This is a controlled local fixture on a shared development host, not a CPU-
isolated host. Ordinary activity and investigation tools coexisted. Host
heartbeat overshoot reached 44.53ms during baseline, and only **9/20 baseline**
and **3/6 stats-control** trials met the predeclared <=20ms scheduling criterion.
All trials remain in the reports. No >450ms spontaneous disturbance recurred.
Python callback timestamps follow GIL acquisition and protobuf parsing, so they
cannot measure a pause before entering Python. The RTP observer is a separate
subscription; native counters improve visibility but are not per-packet NetEq
traces. No universal native bug or leak-freedom claim follows from these runs.

## Reproduce

Use the existing prerequisites and pinned setup in [README.probe.md](README.probe.md).
Choose fresh output directories. No paid providers or system installations run.

```sh
GOCACHE="$PWD/.cache/go-build" go build -o .cache/probe-followup .
.venv/bin/python run.py --binary .cache/probe-followup --diagnostics --repetitions 10 --scenarios continuous dtx --output results/followup-baseline
.venv/bin/python run.py --binary .cache/probe-followup --diagnostics --native-stats --repetitions 3 --scenarios dtx --stall-packet 402 --output results/followup-stall-dtx
.venv/bin/python run.py --binary .cache/probe-followup --diagnostics --native-stats --repetitions 3 --scenarios continuous --stall-packet 566 --output results/followup-stall-continuous
.venv/bin/python run.py --binary .cache/probe-followup --diagnostics --native-stats --repetitions 3 --scenarios continuous dtx --output results/followup-stats-control
.venv/bin/python controlled_analyze.py results/followup-baseline --output results/followup-baseline-analysis.json
```

Run the last analyzer command for each dataset. It uses the unchanged original
analyzer and adds source PCM checks, heartbeat/packet-stage timings, validated
Python callback/frame pairing, observer media alignment, and native counter
intervals. The Python FFI queue hook is diagnostic-only and does not replace
SDK processing. Publisher fault injection preserves the old absolute-deadline
catch-up behavior to reproduce its effect; no sender-policy fix is claimed.

Raw data is retained under ignored `results/controlled-*`. All **210 original
artifact checksums** were reverified, as were original acceptance/summary/CSV
and measurement hashes. `results/full` and its failed results are unchanged.

Validation: focused Go race tests and vet, seven Python analyzer/cleanup tests,
syntax checks, and 32 paired live trials. Three injected content failures are
expected reproductions and are recorded as failed gates, not test passes.
This is investigation evidence, not production rollout or a new full-repository
parity claim. For a recurring Go-specific differential, capture synchronized
native receive statistics and packet arrival traces before changing transport.
