# Synchronized Go/Python receive investigation

The new measurements localize latency variation to output frame phase and native
buffering after nearly simultaneous socket delivery. They do **not** reproduce or
explain the historical 92.269ms Go-over-Python outlier. No production transport or
custom playout policy changed. Original failures and acceptance gates remain intact.

## Experiment and results

23 paired local runs: one traced pilot, two untraced synchronized-stat controls,
ten traced continuous-silence baselines, and ten traced publisher stalls (450ms
before packet 566). Receiver launch order alternated. No builds or numerical
analysis ran concurrently with the measured batches. The host remained shared.

Pins: macOS 27.0.1 ARM64, Go 1.27.4, Python 3.14.4 / RTC 1.1.21,
LiveKit FFI 0.12.80, Rust SDK `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`,
LiveKit server 1.13.11. PCM is mono 24kHz in 50ms frames; Opus RTP clock is 48kHz.
See [pre-run contract](ARRIVAL-CONTRACT.md) and [machine summary](ARRIVAL-SUMMARY.json).

| Batch | Runs | Second onset p95 Go / Python (ms) | Paired Go−Python range (ms) | Original failed runs |
| --- | ---: | ---: | ---: | --- |
| Pilot | 1 | 39.897 / 35.857 | +4.040 | none |
| No-trace controls | 2 | 71.348 / 67.236 | +1.282 to +4.261 | none |
| Baseline | 10 | 87.897 / 87.699 | −46.976 to +41.746 | none |
| Publisher stall | 10 | 327.364 / 331.261 | −6.189 to +45.639 | continuous-00: marker 0 absent in both |

All four batches pass the unchanged aggregate relative-latency gate (Go p95 <=
Python p95 +50ms). This is not an absolute-latency endorsement: stall run04 had
**433.728ms Go / 439.917ms Python** onset latency, with native buffer averages
404/416ms and targets 360ms. All duration, scenario, shutdown and wrapper-error
checks passed; the new content failure remains failed. This follow-up measures
continuous packets through silence and a sender stall, not a new DTX/loss matrix.

All 1500 transmitted media packets map uniquely in both socket traces in every
baseline and nine stall trials. The startup-failed stall trial fails complete
trace attribution and is retained. Valid synchronized stats pairs: baseline
2957/2961, stall 2934/2938 (including the failed trial). Maximum clock-anchor drift
was 3 microseconds. Invalid slots are reported and excluded from attribution.
Maximum absolute marker-packet arrival skew was 0.374ms baseline / 0.174ms stall;
maximum per-run p95 absolute packet skew was 0.318ms / 0.289ms respectively.
These statistics cover complete trace mappings only.

## What explains the new differences

**Frame phase:** baseline run01 reported Go 41.746ms later, although its marker
packet arrived 0.059ms earlier. The marker occupied position 0ms in Go's output
frame and 40ms in Python's. Native interval-average buffer delays were 38/30ms.
An illustrative frame-position adjustment reduces the callback difference from
42.482ms to 2.482ms. This is an estimate with 10ms detector localization, not a
measured per-sample timestamp or replacement acceptance metric.

**Native buffering:** stall run08 reported Go 180.374ms / Python 134.735ms,
a 45.639ms difference. The marker packet reached Go's socket only 0.152ms later.
At synchronized native report time 19.966795s, interval-average buffer delays
were **152.5ms / 78ms**, a **74.5ms difference**, with targets 20ms in both.
Lost and discarded counts were zero at that snapshot. Between the pre-stall
11.267205s snapshot and this snapshot, acceleration removed 14,760 Go samples
versus 17,640 Python samples; deceleration inserted 480 versus zero. Concealed
samples increased 20,280 / 20,640. These are native engine counters, not policies
implemented by this probe.

Marker positions were 20/0ms. The frame-adjusted callback difference estimate
was 65.798ms. Callback-to-consumer p95 was 0.386ms Go / 0.812ms Python (max
1.574/13.287ms). Socket delivery and consumer queue delay cannot account for this
observed differential. Independent native buffering/stretch decisions are
consistent with the evidence; cumulative counters do not identify the exact
internal NetEq cause. Scheduling inside native processing remains unobserved.

**Startup harness failure:** stall run00 began publishing before subscription.
Room-ready events preceded source start by 1509.759/1104.051ms, but track
subscription occurred 222.900/224.239ms after source start. Both socket traces
begin at publisher packet 11 (221.987/221.710ms), contain 1489 literal matching
source headers for packets 11–1499, and omit packets 0–10. Both receivers detect
markers 1–19, missing marker 0. The runner waits for room readiness and uses a
fixed publisher delay; it has no track-subscription barrier. A future harness
change should add that barrier. This run was not shifted, repaired or replaced.

The historical run had equal 30ms marker positions in both frames, so frame
phase alone does not explain its 92.269ms difference. Its original aggregate
50.32ms p95 differential still fails the 50ms gate. New runs cannot reconstruct
its missing native packet-arrival history.

## Instrumentation and limits

A macOS process-local DYLD interposer observes successful recv/recvfrom/recvmsg
returns from owned local server ports 19000–19100. Live traffic uses recvmsg.
Only fixed RTP/SRTP headers and timestamps are retained, never payloads. SSRC
comes from native stats; complete sequence/timestamp progression must map
uniquely to publisher packets. Capacity overflow invalidates attribution.
This is **userspace socket delivery**, not kernel ingress, SRTP authentication
or NetEq admission. The tracer buffers metadata and flushes after FFI disposal;
it never changes packets or drives playout. It requires no root BPF access.

Both receivers sample GetStats on the same absolute monotonic 100ms grid.
Before/after realtime anchors calibrate native report timestamps to that clock.
Valid pairs require request skew <=10ms, response times <=20ms and producer
skew <=20ms; near-onset buffer comparisons also require adjacent valid slots.
Reported buffer delays are differences of cumulative native counters divided by
emitted samples, averaged over the interval, not instantaneous queue lengths.
Native reports can be cached/asynchronous. Python callback observation occurs
after GIL acquisition. Two untraced controls cannot rule out all observer effects.
Shared host scheduling can still influence native behavior; no universal Go or
Python latency advantage is established. Bit-identical parity is not claimed.

## Reproduce and inspect

Use existing [setup and dependency instructions](README.probe.md). No new system
dependencies were installed. The optional tracer is macOS-only and needs clang;
the probe already requires CGO and its pinned native binary. This diagnostic
adds no deployment dependency to RTP Agent.

From this directory, with an unused output path for every run:

```sh
GOCACHE="$PWD/.cache/go-build" go build -o .cache/probe-arrival .
clang -dynamiclib -O2 -Wall -Wextra -Werror -o .cache/receive_trace.dylib trace/receive.c
.venv/bin/python run.py --binary .cache/probe-arrival --diagnostics --native-stats --sync-stats --packet-trace --repetitions 10 --scenarios continuous --output results/arrival-new-baseline
.venv/bin/python run.py --binary .cache/probe-arrival --diagnostics --native-stats --sync-stats --packet-trace --repetitions 10 --scenarios continuous --stall-packet 566 --output results/arrival-new-stall
.venv/bin/python arrival_analyze.py results/arrival-new-baseline --output results/arrival-new-baseline-analysis.json
.venv/bin/python arrival_analyze.py results/arrival-new-stall --output results/arrival-new-stall-analysis.json
```

For controls omit `--packet-trace`, use two repetitions and a fresh output path.
The runner owns a localhost LiveKit server; allow local socket access if sandboxed.
Run measurements without concurrent builds/analysis. Existing results are under
`results/arrival-{pilot,no-trace,baseline,stall}`; each contains a run manifest
with executable/native/source hashes, per-run configuration, native stats, PCM,
heartbeats and (where enabled) packet traces. Analysis sidecars retain complete
synchronized timelines, invalid slots and attribution errors. Raw artifacts stay
local and ignored; the committed summary records hashes and compact evidence.

All **774** files in `results/arrival-preserved.json` were rechecked unchanged,
including prior full/controlled runs and acceptance summaries. No earlier result
was overwritten. This remains an isolated receive-engine investigation, not a
fix for the production incident.
