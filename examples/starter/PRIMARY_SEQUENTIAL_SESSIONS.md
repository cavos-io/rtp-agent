# Five sequential primary-native sessions

PASS: five sequential two-minute sessions in one Go process (PID69019), with
both Go parent and helper built using the race detector. Local LiveKit1.13.11,
macOS27.2.1 ARM64, Go1.27.4; pinned native FFI0.12.80. Native library internals
are outside Go race coverage. No cloud/provider calls were made.

The experimental path now emits10s PCM continuity windows and a final partial
window, plus stable helper cleanup diagnostics after wait/reap. Diagnostics
observe callback delivery; they do not change receive or playback policy.
The default transport is unchanged.

| Cycle | Ending | PCM / wall seconds | Max gap ms | Goroutines | Live heap MiB | RSS MiB | Helpers left |
|---|---|---|---|---|---|---|---|
| 1 | session close | 120.000 / 120.0001 | 68.678 | 4 | 4.379 | 89.23 | 0 |
| 2 | publisher disconnect | 120.000 / 120.0003 | 66.477 | 4 | 4.440 | 98.27 | 0 |
| 3 | track unsubscribe | 120.000 / 120.0007 | 142.116 | 4 | 4.515 | 105.05 | 0 |
| 4 | session close | 120.000 / 120.0001 | 57.047 | 4 | 4.501 | 118.53 | 0 |
| 5 | track unsubscribe | 120.000 / 120.0000 | 53.647 | 4 | 4.514 | 100.55 | 0 |

All600 one-second continuity windows passed the existing250ms tolerance;
maximum absolute window error6.314ms. Exactly two participants were present in
each session; expected caller-only/empty state remained after shutdown. Both
outgoing tone markers survived; drain and interruption-clear gates passed.
Every helper exited0, was reaped without a kill, and reported native disconnect,
handle release and runtime disposal with no callback overflow. No race reports
or native failures occurred. Full session cleanup took3.048–3.269s, below5s.

There were60 periodic reports,5 final windows and5 helper-cleanup reports.
The first diagnostic window includes helper startup (~0.31–0.51s to first PCM);
the final window includes teardown. They are not steady-state drift failures.
Parent cleanup timing starts after cancellation or read completion: normal source
departure has already completed native teardown by then. Native teardown itself
was2.342–8.256ms; explicit-stop parent wait was~1.02s (race helper).

After3s settle and GC, goroutines stayed4 and live heap increased137.6KiB from
cycle1 to5. RSS fluctuated89.23–118.53MiB and ended100.55MiB. This sample shows
no accumulating goroutine/helper leak or large live-heap growth; it does not
prove long-term native heap leak freedom. Silence was sent in continuous RTP
packets; this run does not establish DTX/loss behavior or acoustic latency.

All3038 pre-existing artifact hashes verified unchanged, including earlier
failed runs and cloud evidence. New raw logs, per-session recordings/reports,
600 windows, resource snapshots, frozen gates, binary hashes and summary live in
`../native-receive-probe/results/primary-sequential-20261116/`.
Reproduction commands and diagnostic field definitions: [NATIVE_ROOM.md](NATIVE_ROOM.md).

Validation: native-room/starter unit race tests (three repeats); targeted
continuity, forced-kill, nonzero-exit and shutdown flag race tests (three repeats);
three real-native short lifecycle cases; five-session live race test; vet,
architecture and targeted staticcheck/deadcode; explicit test-integrity checks.
Local application build passed (nonfatal sandbox module-cache-write warning).
No full parity-suite pass is claimed; the previously documented wrapper issue
remains a separate follow-up. No hooks were bypassed.

Artifact SHA256:

- `five-sessions.log`: `7ff0d11c3bb605c7f6c540a2c8d8c81a5846e4e6d21850da8c4660e1b6a1d8a4`
- `summary.json`: `0f49e89440263848a71ff18b647e9d47302618dc74148234b11c9e01024dee53`
- `gates.txt`: `873a2bff03008f262d44b07e29043cd6163e249bd43b4174647b8bd7d94da9e3`
- `environment.json`: `4f87f32d3615213f80e5a67c602e3c6d70ef9ce601f9ee91a3c4fb06061dccf4`
