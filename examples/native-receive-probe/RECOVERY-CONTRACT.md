# Recovery analysis contract

Post-hoc analysis of the existing ten barrier-baseline and ten barrier-stall
captures, using unchanged live PCM, packet traces and synchronized native stats.
No new receive policy, publisher scenario, acceptance gate or measurement capture.
Freeze these supplemental definitions before computing recovery scores.

- Reference time zero: actual injected-stall end; baseline controls use source
  start +11.770s as a comparable observation origin (not an injected event).
- Native buffer reference: median interval-average buffer delay in source seconds
  8–11, separately for each receiver/run. Recovery threshold: reference +20ms.
  Report target-delay recovery separately with the same rule.
- Native recovery: first post-origin contiguous period lasting at least 2s with
  every interval at/below threshold. Report beginning and confirmation time,
  peak delay, later excursions, and observation end. Invalid synchronized samples,
  nonadjacent slots or capture intervals outside 50–150ms break continuity.
  Use only intervals bounded by valid synchronized producer samples and stop at
  source time30s. No interpolation across missing observations. If no qualifying
  period occurs, report not confirmed within the observed window (right censored).
- PCM content latency: all detected marker frames, actual publisher emission to
  callback, plus original delivery timestamps for comparison. Python callbacks
  are paired with frames using the existing strict callback_pairing guard.
  Reference = p95 callback latency for markers2–9; threshold = reference +50ms
  (50ms application-frame quantization). Recovery evidence requires three
  consecutive post-silence markers at/below threshold. Report first qualifying
  marker emission time and third-marker confirmation time, relative to origin.
  This is sparse observational evidence, not precise recovery time: no content
  markers exist during source seconds10–20. First marker already normal means
  recovery was observed by that marker, not that it occurred then.
- Preserve unchanged acceptance scores, all failures, and raw artifact hashes.
  Use fresh sidecars; report every run and censoring. Existing frame-position
  estimates must not replace raw latency gates. Native counter recovery is not
  proof of content fidelity; marker ordering is checked independently.

Stop when existing traces yield bounded recovery measurements. If the end of
capture prevents confirmation, state the bound rather than infer a recovery time.

Numerical clarification during analyzer verification: native delay counters are
floating-point seconds, so threshold comparisons allow 1e-6ms roundoff. A failing
regression exposed spurious excursions at 40.0000000000005ms against 40ms. This
is one-nanosecond numerical tolerance, not a latency tolerance change. Initial
analysis sidecars are retained; corrected scores use recovery-*-v2.json.
