# Subscription barrier: controlled rerun

The isolated harness now publishes the track, waits for both Go and Python
`subscribed` events, then starts sending the fixture. Room readiness alone no
longer releases the publisher. Both events are written after their native audio
streams are created. A fresh, exclusively created release file connects the
runner to the publisher; the publisher wait is cancellable and limited to 20s.
No media is sent while waiting. Standalone publishers without the runner's
`PROBE_START_BARRIER` retain their previous startup delay.

This is a startup handshake, not a playout policy. Production transport,
receiver processing, fixture content, publisher packet cadence, stall injection,
and existing acceptance gates are unchanged.

## Live results

Ten continuous-silence baselines and ten 450ms publisher stalls before packet566,
with the same synchronized statistics, socket tracer, alternating receiver
order and version pins as [ARRIVAL.md](ARRIVAL.md). No builds or numerical
analysis ran during capture. Contract: [BARRIER-CONTRACT.md](BARRIER-CONTRACT.md).

| Measurement | Baseline (10 runs) | Stall (10 runs) |
| --- | ---: | ---: |
| Runs passing original per-run checks | 10/10 | 10/10 |
| Both subscriptions precede release/start/packet0 | 10/10 | 10/10 |
| Complete 1500-packet native traces in both receivers | 10/10 | 10/10 |
| Second-speech onset p95 Go / Python | 79.367 / 81.247ms | 305.230 / 304.176ms |
| Paired Go−Python onset range | −44.136 to +37.600ms | −48.929 to +6.509ms |
| Original relative-latency gate | pass | pass |
| Subscription-to-first-frame p95 Go / Python | 49.363 / 47.445ms | 49.784 / 49.134ms |
| Shutdown p95 Go / Python | 7.442 / 14.468ms | 3.804 / 15.670ms |
| Valid synchronized stats pairs | 2844/2848 | 2845/2851 |

Both receivers retained all 20 ordered markers in every run, including marker0.
Subscription events preceded source start by 4.193–66.110ms across all receivers.
All packet0 headers were present, with SSRC matched to native stats, and full
trace attribution succeeded in every run. These observations close the startup
race found in the earlier `arrival-stall/continuous-00` capture for this rerun.

The stall still causes substantial absolute latency: maximum measured onset
was 354.858ms Go / 355.194ms Python. A passing relative gate does not establish
uniformly acceptable absolute latency or bit-identical parity. The historical
92.269ms differential and earlier failed content/latency checks remain unchanged.
This rerun does not claim the production incident is fixed.

## Preservation and verification

All **2523** existing result/contract/summary files listed in
`results/barrier-preserved.json` were SHA-256 verified unchanged after capture.
Earlier failed runs were neither overwritten nor relabeled. New raw artifacts
are local and ignored under `results/barrier-baseline` and `results/barrier-stall`.
Their run manifests pin executable/native/source hashes. Committed
[BARRIER-SUMMARY.json](BARRIER-SUMMARY.json) contains original checks, subscription
ordering evidence, trace packet0 presence, manifest hashes and measurements.

Focused verification: Go race tests and vet; 12 Python tests; Python syntax;
source/diff review. Regression coverage verifies that a missing release blocks
the publisher, cancellation terminates the wait, room readiness alone cannot
release it, and an exited receiver prevents release. No paid providers or new
system dependencies were used. The macOS tracing/clock attribution limits from
ARRIVAL.md still apply.

## Reproduce

Use the existing setup and pinned native library. Run from this directory and
choose fresh output paths; the runner rejects existing result directories.
The tracer is the unchanged `.cache/receive_trace.dylib` built in ARRIVAL.md.

```sh
GOCACHE="$PWD/.cache/go-build" go build -o .cache/probe-barrier .
.venv/bin/python run.py --binary .cache/probe-barrier --diagnostics --native-stats --sync-stats --packet-trace --repetitions 10 --scenarios continuous --output results/barrier-new-baseline
.venv/bin/python run.py --binary .cache/probe-barrier --diagnostics --native-stats --sync-stats --packet-trace --repetitions 10 --scenarios continuous --stall-packet 566 --output results/barrier-new-stall
.venv/bin/python arrival_analyze.py results/barrier-new-baseline --output results/barrier-new-baseline-analysis.json
.venv/bin/python arrival_analyze.py results/barrier-new-stall --output results/barrier-new-stall-analysis.json
```

In each run, compare `subscribed.at` in `go.jsonl` and `python.jsonl` with
`subscription_barrier_released.at`, `start.at` and packet0's `at` in
`publisher.jsonl`. Both subscriptions must precede all three publisher events.
Native trace SSRC/header mapping must include packet0 and all 1500 media packets.
