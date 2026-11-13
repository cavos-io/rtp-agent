# Predeclared measurement contract

Frozen before interpreting pilot measurements. Ten 30-second runs per scenario.
Go/Python use 24kHz mono PCM, 50ms application frames, identical native FFI binary.
Opus RTP uses 48kHz timestamps. No receive-side filler or custom timing policy.

- Matched sustained wall window: publisher start +1s through +30s (29 seconds).
  Startup is reported separately. Count callback-delivered samples in this same window.
- Go/Python PCM duration difference <=50ms; each receiver's deficit <=100ms.
- Startup, second-content onset, and callback-gap p95: Go <= Python +50ms.
- Report absolute timings, maximum gaps, and every gap >100ms.
- Marker IDs 0..19 must each occur once in order. The 500ms loss at 24.5..25s avoids markers.
- Shutdown <=2s and confirmed native disconnect; no wrapper overflow or errors.
- DTX verified by encoder InDTX, <=2-byte non-transmission output, publisher omission,
  RTP timestamp gaps, and continuous sequence numbers. Loss advances sequence numbers.
- Same host CLOCK_MONOTONIC via clock_gettime in both runtimes. Timestamp granularity
  measured separately; marker localization is 10ms, delivery quantized by 50ms frames.
- Observer RTP is from an independent subscription, not direct visibility into native reception.
- Python timestamps frames on iterator delivery; Go records native callback entry. Report
  this reference-wrapper scheduling difference; do not claim identical callback layers.
- FFT marker analysis is content-order evidence, not a full speech intelligibility score.
- First-frame startup is measured from track subscription to first PCM delivery.
  Active native tracks can emit PCM before the first RTP packet; count those frames
  separately. First content latency uses marker 0 versus its actual publisher emission.
