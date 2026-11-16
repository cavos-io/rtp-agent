# Controlled local in-process false-interruption recovery

2026-11-16, macOS arm64. `examples/local` startStarterSession wiring, real native input/output, local LiveKit server, Go race detector. No paid providers. Default transport and helper fallback unchanged.

## Evidence and limits

PASS: run-03.log. The test attaches a separate provider-free core AgentActivity controller to the actual local session native playback/output hooks. Injected start/end-of-speech without a transcript exercises the two-second false-interruption timer. This proves controller/native playback integration, not acoustic VAD classification or an end-to-end cloud provider pipeline.

| Check | Actual | Gate |
|---|---:|---|
| Same speech resumed, not interrupted | true | required |
| Recovery after end-of-speech | 2002.442 ms | 1900–3000 ms |
| Input PCM / wall | 10 / 10.00017775 s | duration difference ≤250 ms |
| Maximum input callback gap | 29.496 ms | ≤250 ms |
| Output marker frames, 990 / 1320 Hz | 50 / 54 | both present |
| Paused output | passed | 300 ms drain then 200 ms without markers |
| Pending publication | passed | blocked until resume |
| Participants during / after agent close | 2 / 1 | only caller remains |
| Per-room cleanup | 2.523 ms | handles released, disconnect callback |
| Callback overflow | false | false |
| Process-global runtime disposal | true at test end | required |
| Race detector | passed | no Go race reported |

Drain and hard-clear playback accounting assertions passed. Per-room runtime_disposed=false is expected; global disposal follows at test end. No helper process is used.

## Preserved attempts

- run-01: recovery worked; failed the room-deletion assertion because FakeJob intentionally skips DeleteRoom.
- run-02: changing FakeJob=false caused startup readiness to time out at 45 seconds. This alternative fixture is not validated; no recovery claim for this attempt.
- run-03: restored fake-job harness and asserted native agent departure instead of automatic room deletion. Recovery, PCM, content and playback gates unchanged. Room deletion behavior is not covered by this run.

Cloud RM_emUCVQLRGAQg inputs retained under cloud-RM_emUCVQLRGAQg with SHA256 manifest. Preservation check verified all 3349 prior result files plus originals and copies of the three cloud inputs unchanged. All attempted runs retained.

## Reproduce

Start `livekit-server --config examples/native-receive-probe/results/reconnect-trace-20261116/server.yaml` in a separate terminal. Then, from repository root (choose a fresh results directory):

```sh
GOCACHE="$PWD/examples/native-receive-probe/.cache/go-build" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/local-false-recovery-new" \
go test -race -tags 'nativeinprocess localrecoveryintegration' ./examples/local \
  -run '^TestLocalNativeFalseRecovery$' -count=1 -timeout 60s -v
```

Native FFI 0.12.80; Rust revision 5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb; dylib SHA256 41ad8dfc42f2cf0e959231947556d961e49f9d402511a96ee3c230463139d735.

The local example is Git-ignored. This checkpoint includes its exact test-source snapshot; restore it to examples/local/native_recovery_integration_test.go to rerun. The in-process integration remains separate uncommitted workspace work and is required to run this test. This evidence checkpoint is not a standalone integration release.

Raw logs, cloud archive and failed attempts remain under examples/native-receive-probe/results/local-false-recovery-20261116; artifacts-sha256.json records every artifact. They are preserved locally, not included as raw cloud data in Git. The successful full test log records exit success; no new live run was performed for this checkpoint. Existing raw reports were not edited.
