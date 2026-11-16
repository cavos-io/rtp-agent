# Native reconnect lifecycle fix — 2026-11-16

## Diagnosis

The trace reproduced the original failure: native emitted Reconnecting, failed
signal resume on the injected503, then began full restart and emitted selected
TrackUnsubscribed. The helper returned errTrackUnsubscribed immediately, tearing
down the native engine before it could retry. This was application lifecycle
classification, not a need for custom receive playout.

Pinned rust-sdks5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb confirms
room/mod.rs::handle_restarting removes remote participants/subscriptions;
handle_restarted republishes local output using the existing source/Track Arc.
The native engine owns retry scheduling. Inspection used the cached pinned source;
refs/ was not modified.

## Changes

The isolated helper recognizes reconnecting state. Selected-track unsubscribe
within that state releases the old stream/track handles and waits for native
resubscription; restart-induced participant departure no longer ends the session.
Only the configured participant and exact track SID are accepted. Resubscription
uses a separate IPC event, avoiding a duplicate initial readiness barrier. Frames
from retired stream handles are ignored and their event-owned resources released.
A30s lifecycle bound ends the helper if the selected track never resubscribes.
Normal source departure outside reconnect and terminal room disconnect still end
or fail the session. No custom packet timing, concealment or PCM filling is added.

Sanitized stderr tracing records native event type, monotonic timestamp and
handles; proxy tracing records request path and blocked state. Neither logs token
payloads/query strings. Native and parent clock timestamps are not subtracted.
Default production transport, provider adapters and native retry policy unchanged.
The helper must be rebuilt to use this fix.

## Evidence, including remaining failures

Artifacts: examples/native-receive-probe/results/reconnect-trace-20261116/.
trace-01 reproduces the original early exit with tracing. fix-01 demonstrates
reconnection and resumed PCM but fails the original uninterrupted timeline gate:
8.10s PCM /10.001s wall, maximum gap1.940s. That original gate remains in place.

A distinct full-restart recovery test was added with predeclared limits: signaling
handshake within5s, PCM deficit and callback gap <=3s, final three one-second
windows within250ms of wall with nonzero content energy, both output markers,
two participants before disruption, and clean native/helper shutdown. The3s gap
limit remains failing; it was not increased after observing these results.

| Trial | New signaling handshake | PCM / wall | Max callback gap | Outcome |
|---|---:|---:|---:|---|
| recovery-01 | 3.071s | 7.050 /10.000428s | 3.028s | Recovered; gap gate failed |
| recovery-02 | 3.881s | 6.150 /10.008215s | 3.913s | Recovered; gap/deficit gate failed |

Both reached native Reconnected and TrackSubscribed with new stream handles.
Both passed the final three PCM/content windows, output markers (50 and44 resumed
marker frames), exit0, reap, native handle/runtime release and no forced kill.
Signaling handshake timing is not acoustic recovery timing. Lost interval PCM is
not reconstructed. Recovery is functional, but bounded-gap/default-readiness
acceptance is NOT fully passing. Native retry/full-restart behavior determines the
remaining media outage; no retry or playout tuning was introduced to hide it.

Normal session close, publisher disconnect and track unsubscribe live regressions
all passed under the race detector. Existing same-SID selection intentionally does
not provide seamless replacement of a publisher track with a different SID.
The30s no-resubscription timeout is not exercised by the short live scenarios.

## Regression checks and preservation

- New native lifecycle unit tests replay restart unsubscribe/departure followed by
  reconnect and verify subsequent real departures remain terminal. Unrelated
  participants/tracks never reset the selected stream. Passed10 repetitions.
- Full probe unit suite and parent nativeroom/starter suites passed with -race.
- Tagged starter vet, test-integrity and architecture checks passed.
- No native internal race-detector coverage or full repository parity pass claimed.
- All3176 pre-existing result artifacts, including both original outage failures,
  preserved by a pre-run hash manifest. Existing unrelated mock-counter race,
  parity-wrapper failure and provider errors remain documented separately.

## Repeat

Start the preserved local server configuration. Rebuild the helper:

```sh
go -C examples/native-receive-probe build -race -o .cache/probe-reconnect .
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/reconnect-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-reconnect" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestNativePrimarySignalOutageRecovery$' -count=1 -timeout 90s -v
```

Use a new directory every time. TestNativePrimarySignalOutage preserves the stricter
original no-gap contract. Both can fail as documented; neither is silently skipped.
Keep native opt-in. The next decision is whether native full-restart outage meets
the product's interruption tolerance or requires a supported upstream improvement;
it cannot be made continuous by inventing PCM in the wrapper.

## Accepted experimental limitation

The user accepted the observed roughly3–4s media interruption during native full
reconnect for now. This is acceptance of a known limitation for the opt-in path,
not a claim that the original uninterrupted-PCM or3s gap gates pass. Preserve
those failures and do not insert PCM to conceal them. It does not authorize a
default-transport switch. The subsequent Python Agents comparison also observed
multi-second gaps and one5s signaling deadline failure; see
[PYTHON-OUTAGE.md](../native-receive-probe/PYTHON-OUTAGE.md).
