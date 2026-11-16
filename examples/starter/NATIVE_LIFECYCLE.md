# Native lifecycle/reconnect investigation — 2026-11-16

Local provider-free tests, race-instrumented parent and existing race-built Go
helper, unchanged FFI0.12.80 and server1.13.11 on macOS ARM64. Only tagged test
harness files changed. Default transport and native receive policies unchanged.

## Results

| Scenario | Result | PCM / wall seconds | Max input gap |
|---|---|---:|---:|
| Native signaling WebSocket closed, immediately available | Pass | 10 / 10.000069 | 79.88ms |
| Caller SDK signal reconnect | Pass | 10 / 10.000163 | 54.30ms |
| Caller track unpublished then new SID published | Clean session termination, not seamless recovery | 5 / 5.000064 before replacement | 56.94ms |
| Native signaling unavailable for2s (HTTP503) | Fail, twice | PCM stopped after fault | Existing250ms window gate failed |

Successful runs retained both output tone markers, two participants during media,
and clean native teardown/reap (exit0, no kill). Native replacement WebSocket
handshake appeared1.915ms after closure; caller OnReconnected arrived5.933ms after
simulation. Those timings have different endpoints: neither is an acoustic
recovery latency. The native test observes HTTP101 and subsequent media/cleanup,
not a native Reconnected callback.

Two-second outage trials both logged native resume failure on HTTP503 followed by
`restarting connection... attempt: 2`. No new WebSocket handshake arrived within
the predeclared5s bound, and subsequent one-second input windows had zero PCM.
This proves the recovery gate fails for this fixture; it does not yet isolate
native retry behavior versus adapter termination on unsubscribe/full restart.
The adapter currently ends on selected-track unsubscribe or participant departure
and treats terminal room disconnect as an error. It does not rebind replacement
track handles. Fixing or changing that behavior is outside this validation task.

The test proxy affects only the native agent's signaling, including HTTP requests
on that endpoint; RTP stays direct. A503 is a controlled server-unavailability
case, not a simulation of total packet loss, Wi-Fi loss, ICE restart or node loss.
Passing signal-only recovery cannot establish full network recovery.

## Gates and limitations

Existing total/per-second PCM discrepancy and callback gap limits250ms retained;
at least20 decoded frames of each output tone; two participants; native cleanup
flags and exit0 required on successful scenarios. Signal recovery bound5s;
controlled outage2s. No failure is skipped or reclassified as a pass. The outage
test intentionally remains a failing opt-in integration gate until investigated.
Publisher goroutines are canceled/joined on early test failure.

Full default nativeroom/starter race suites and tagged starter vet passed during
this investigation. No live race report was observed. Earlier documented mock
counter race and parity-wrapper issues remain separate. Helper crash, complete
media outage, server restart/migration, longer retry windows and seamless track
replacement remain unvalidated here. No default promotion recommended.

## Evidence and repeat commands

All artifacts are under
`examples/native-receive-probe/results/primary-lifecycle-20261116/`.
Both failed outage logs are retained as native-outage-01.log and
native-outage-02.log. Passing scenarios have summary.json and recordings/reports.
The latest cloud RM_6FLRAaxjq569 ZIP/report/log were copied with SHA256 hashes;
the pre-run manifest covers3148 earlier artifact files.

With the preserved server.yaml running locally, choose one test and a fresh
NATIVE_ROOM_RESULT_DIR per invocation:

```sh
NATIVE_TEST_URL=ws://127.0.0.1:18991 \
NATIVE_ROOM_RESULT_DIR="$PWD/examples/native-receive-probe/results/lifecycle-manual-01" \
RTP_AGENT_NATIVE_HELPER="$PWD/examples/native-receive-probe/.cache/probe-primary-pause" \
LIVEKIT_LIB_PATH="$PWD/examples/native-receive-probe/.cache/native/liblivekit_ffi.dylib" \
go test -race -tags nativeroomintegration ./examples/starter \
  -run '^TestNativePrimarySignalReconnect$' -count=1 -timeout 90s -v
```

Other selections: TestNativePrimaryPublisherSignalReconnect,
TestNativePrimaryTrackReplacementEndsSession, TestNativePrimarySignalOutage.
The last currently fails. The native proxy refuses non-local targets. No cloud
project, paid inference, host firewall, or system network changes are needed.

Next: instrument native reconnect/full-restart and selected-track lifecycle
ordering, then reproduce the2s failure before choosing an integration change.
A manual cloud network test can follow that fix; it is not required to reproduce
this blocker.
