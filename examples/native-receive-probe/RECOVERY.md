# Native latency recovery after the publisher stall

Native buffering recovered over seconds, not immediately after packets resumed.
In the ten existing subscription-barrier stall trials, the first sustained
return to within 20ms of each receiver's pre-stall buffer reference began at a
median **9.56s in Go**. Python's median was **9.13s among eight confirmed trials**;
two Python trials remained unconfirmed when observation ended about 18.2s after
the stall. The unequal confirmed subsets do not establish a Python advantage.

This is new analysis of existing live data, not a new live run. Nothing changed
in the publisher, receive engine, transport or original acceptance gates.
[RECOVERY-CONTRACT.md](RECOVERY-CONTRACT.md) defines the supplemental metrics;
[RECOVERY-SUMMARY.json](RECOVERY-SUMMARY.json) records every run and input hashes.

## Definition and measured recovery

Time zero is the actual end of the 450ms publisher stall before packet566
(approximately source second11.77). Native reference is the median interval-average
buffer delay during source seconds8–11, independently per receiver/run. Recovery
requires every valid interval to remain <= reference +20ms for at least two
seconds. Invalid samples/gaps break confirmation. Samples use the existing
synchronized producer timestamps, approximately 100ms apart. This measures a
window-average native delay, not instantaneous queue depth or exact PCM latency.

| First qualifying two-second period | Go | Python |
| --- | ---: | ---: |
| Native buffer: confirmed trials | 10/10 | 8/10 |
| Native buffer: median period beginning | 9.558s | 9.129s, confirmed subset |
| Native buffer: beginning range | 8.769–16.170s | 8.634–16.170s, confirmed subset |
| Native buffer: median confirmation time | 11.614s | 11.179s, confirmed subset |
| Native target: median period beginning | 4.822s | 4.831s |
| Native target: beginning range | 4.746–11.569s | 4.771–11.569s |

Target-delay recovery is not actual buffer recovery. Both targets had later
above-threshold excursions in trials01 and03 after their first qualifying
period. No confirmed buffer recovery subsequently exceeded its threshold before
capture ended. This is bounded follow-up, not a guarantee of permanent stability.

| Stall trial | Buffer period begins Go / Python, seconds after stall end | First qualifying PCM marker Go / Python |
| --- | ---: | ---: |
| 00 | 8.840 / 8.840 | 10 / 10 |
| 01 | 16.170 / 16.170 | 11 / 11 |
| 02 | 9.982 / unconfirmed | 15 / unconfirmed |
| 03 | 15.218 / unconfirmed | 17 / 11 |
| 04 | 9.045 / 9.146 | 10 / 10 |
| 05 | 11.729 / 11.028 | 11 / 11 |
| 06 | 8.769 / 8.770 | 10 / 10 |
| 07 | 12.169 / 12.171 | 14 / 14 |
| 08 | 9.134 / 8.634 | 10 / 10 |
| 09 | 8.913 / 9.113 | 11 / 10 |

Python trial02 had no confirmed buffer recovery by +18.182s. In the last second,
its interval averages ranged 54–58ms against a 55.5ms threshold. Its marker
latency remained approximately 93–97ms late in the run, near but mostly above
its separate 93.18ms content threshold. This is a threshold-bound result, not
proof that latency stayed hundreds of milliseconds high.

Python trial03 had no confirmed buffer recovery by +18.218s. It reached 50ms
against a 50ms threshold only near the end, leaving less than two seconds to
confirm. Its content criterion did pass earlier, illustrating that the native
buffer and marker criteria measure different things. Neither censored run is
assigned an inferred recovery time; longer captures would be needed to confirm.

## Trajectory and PCM evidence

Median across ten trials of each run's median native buffer delay in the stated
one-second window:

| Window after stall end | Go | Python |
| --- | ---: | ---: |
| +1 to +2s | 460ms | 460ms |
| +2 to +3s | 460ms | 460ms |
| +4 to +5s | 460ms | 460ms |
| +8 to +9s | 100.5ms | 101.5ms |
| +12 to +13s | 45ms | 42.5ms |
| +16 to +17s | 31ms | 39.5ms |

PCM evidence uses actual publisher emission to the callback carrying each marker,
with Python's callback/frame pairing checked before comparison. The reference
is p95 latency of pre-stall markers2–9; the tolerance is +50ms for application
frame quantization. Three consecutive post-silence markers must satisfy it.
Original iterator-delivery latency and marker position remain in the analysis
sidecars; no frame-phase adjustment replaces the measured latency.

All ten Go trials and nine Python trials provided this content-recovery evidence.
First qualifying marker emission was observed +8.23 to +15.23s in Go and +8.23 to
+12.23s in the nine qualifying Python trials, with confirmation two markers
later. These are sparse observation bounds: the fixture has no content markers
between source seconds10 and20. Marker10 occurs about +8.23s after stall end.
If it already meets the criterion, recovery is observed by that marker; we
cannot say it first happened then. Native stats are the only recovery evidence
during the silence interval. All 20 markers remained present and ordered in
both receivers in every original trial.

## Controls, preservation and limits

Nine of ten uninjected baseline trials were within their buffer threshold at
the first post-origin sample. Baseline06 instead needed approximately +8.759s
in both receivers. It had a 43.227ms publisher scheduling delay at packet458
and up to 17.501ms inter-receiver socket-arrival skew. This supports caution
about shared-host variability; it does not establish the exact internal cause.
All baseline content checks met the first available post-silence marker criterion.

The original acceptance summaries are byte-value equivalent to those recorded
in BARRIER-SUMMARY.json: all 20 original per-run and aggregate gates still pass.
Every one of **2899** existing result/contract/summary files in
`results/recovery-preserved.json` was hash-verified unchanged. Historical failed
runs remain failed. The initial supplemental analysis sidecars are also retained:
a regression test caught floating-point counter roundoff falsely treating
40.0000000000005ms as above 40ms. Corrected analysis uses a 1e-6ms numerical
epsilon and writes new `*-v2.json` files; original acceptance logic is untouched.

Versions remain macOS27.0.1 ARM64, Go1.27.4, Python3.14.4 / RTC1.1.21,
FFI0.12.80, Rust SDK `5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb`, LiveKit server1.13.11.
Both receive engines use the same native binary, separate connections, and
24kHz/50ms application PCM. Native stats are asynchronous/cached; valid sample
selection does not eliminate all observer effects or native thread scheduling.
Python callback timestamps follow GIL acquisition. No general Go/Python latency
ordering, exact NetEq decision mechanism, or production incident fix is claimed.

## Reproduce

From this directory, use fresh output names; the analyzer exclusively creates
its output and reads input captures without writing to them:

```sh
.venv/bin/python -m unittest discover -p 'test_*.py'
.venv/bin/python recovery_analyze.py results/barrier-baseline --output results/recovery-baseline-new.json
.venv/bin/python recovery_analyze.py results/barrier-stall --output results/recovery-stall-new.json
```

No new dependencies or native builds are required. Full per-marker series and
all per-run recovery measurements are in the analysis sidecars. Five new
regression tests cover sustained recovery, later excursions, invalid samples,
sample gaps, censoring, marker continuity and numerical threshold precision.
