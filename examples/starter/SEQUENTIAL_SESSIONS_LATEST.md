# Latest five-session native receive verification

PASS on revision `4b6b062bb5f235092f91f12add1d420b4ed5c1c8`.
One Go process (PID 21449) ran five sequential two-minute sessions with the race
detector. Local LiveKit 1.13.11 and a newly built Go helper received real Opus tone
PCM at the agent-input hook. No paid providers ran. The helper itself used a
normal build; the native library is not covered by Go's race detector.

All frozen acceptance gates remained unchanged from the prior run. No source,
transport, receive policy or test implementation was modified for this rerun.

| Cycle | PCM / wall seconds | Max callback gap ms | Settled goroutines | Live heap MiB | RSS MiB | Helper children |
|---|---|---|---|---|---|---|
| 1 | 120.000 / 120.000 | 55.755 | 2 | 4.596 | 158.906 | 0 |
| 2 | 119.950 / 120.002 | 54.656 | 2 | 4.734 | 112.297 | 0 |
| 3 | 120.000 / 120.000 | 111.308 | 2 | 4.707 | 133.094 | 0 |
| 4 | 120.000 / 120.000 | 55.762 | 2 | 4.754 | 147.219 | 0 |
| 5 | 120.000 / 120.001 | 54.448 | 2 | 4.829 | 155.500 | 0 |

All 600 one-second continuity windows passed; maximum rounded-log difference 52ms.
Tone energy was present each session; all unpublish-triggered shutdowns completed
within the 5s gate. Maximum callback gap 111.308ms passed the unchanged 250ms gate.
Cycle 2's 50ms sample-count difference is one PCM frame at the measurement boundary;
this measurement does not establish whether a frame was lost.

Goroutines stayed at 2 after each 3s settle and GC. Live heap increased 238.3KiB
from cycle 1 to 5; RSS fluctuated 112.3–158.9MiB and ended below cycle 1. No helper
remained after any cycle or after the complete run. This five-cycle sample shows
no large accumulating resource growth; it is not proof of long-term native/cgo
leak freedom. No race reports appeared. This native test does not exercise a
multimodal provider; its separate lifecycle regression results remain applicable.

All 2940 prior artifact hashes verified unchanged. New logs, contract, environment
and binary hashes, summary and preservation manifest live in
`../native-receive-probe/results/sequential-sessions-latest/`. The test-owned
server was stopped after completion.

Reproduction: use the local server configuration from SEQUENTIAL_SESSIONS.md,
then run from repository root:

```sh
go -C examples/native-receive-probe build -o .cache/probe-helper-latest .
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-helper-latest" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativereceiveintegration \
  -run '^TestNativeSequentialSessions$' -timeout 14m -count=1 -v ./examples/starter
```

Checkpoint artifact SHA-256 (raw artifacts remain in the local results directory):

- `five-sessions.log`: `7f3083589f77a312bc1bf3fc68f5d0487957706fa014509c938594d7bdfeaa7a`
- `summary.json`: `4d9eb064572846f362999fbf6c835a7d46256e5c3f9234d0c6db0b76990e5e6b`
