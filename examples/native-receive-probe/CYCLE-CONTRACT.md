# Same-process lifecycle investigation contract

Scope: the isolated native receive probe, pinned FFI 0.12.80 on macOS ARM64.
No production transport, STT, interruption or receive/playout policy changes.

Compare two explicitly different lifetimes:

1. `process`: initialize native FFI once; connect, subscribe, receive PCM, close
   stream/track/room 100 times; dispose FFI at the end.
2. `cycle`: initialize, connect, receive PCM, disconnect and dispose 100 times
   in the same Go process. This also tests pinned-runtime reinitialization.

One continuous Python RTC publisher supplies a real tone track to every cycle.
It is a fixture publisher, not a replacement receiver. Each cycle runs for one
second including connection setup and must receive at least one PCM frame.

Capture complete goroutine stacks before initialization, immediately after each
room closes, after 100ms settling, and immediately/1s/5s after final disposal.
The settling waits are teardown measurements, not audio timing policies.

Pass requirements:
- Every cycle completes without native request/drop/disconnect errors or queue overflow.
- Every settled callback queue is empty.
- Go-owned stack count returns to the baseline after every cycle and at the end.
- Foreign callback contexts must plateau: after the first 20 warmup cycles,
  their count must not exceed the warmup maximum.
- Classify a foreign context only for the exact three-line stack with
  `[syscall, locked to thread]`, `runtime.goexit({})`, and its source location.
  Arbitrary blocked syscall/cgo/locked stacks remain visible as Go-owned/other.
- All cycles run in the same PID for each lifetime experiment.

Counts alone do not prove native heap leak freedom. Match the actual stack
shape to the pinned Go runtime and FFI source before assigning a mechanism.
No original audio/latency acceptance gate is changed by this investigation.
