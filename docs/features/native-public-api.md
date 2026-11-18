# Public in-process native transport

The experimental transport is now importable by ordinary downstream modules:

```go
import "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
```

Build with `-tags nativeinprocess` and CGO. macOS arm64 STG calls and Linux arm64/emulated amd64 image checks are recorded below. The public constructor and shutdown API retain their existing signatures. Moving ownership does not make native the default or make all downstream workflows compatible.

## Session startup

Use this inside your opt-in downstream job startup branch, with the existing session and API credentials. Keep the current StartSession path for the default branch.

```go
transport := nativeroom.NewInProcess(job, session, nativeroom.Config{
    Library:     libraryPath, // absolute path to pinned liblivekit_ffi.dylib
    Participant: callerIdentity,
}, apiKey, apiSecret)

opts := applicationRoomOptions // preserve captions, callback and recording options
opts.DisablePreConnectAudio = true // explicit restriction of the native test profile
err := job.StartSession(ctx, session, worker.StartSessionOptions{
    SessionContext: ctx,
    Transport:      transport,
    RoomOptions:   opts,
})
```

The examples and the current local agent-worker-golang integration interpret `RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS`; JobContext does not interpret it automatically. `applicationRoomOptions`, caller identity and library selection are supplied by your composition code.

Do not call `job.Connect` first. Custom transport requires an unconnected Pion job room and joins using the assigned primary token. A selected microphone is required; multiple initial microphone candidates require explicit participant selection. `Config.URL`, `Token`, and `TrackSID` overrides exist for fixtures; ordinary workers should use assigned job connection information and discovery.

The transport implements `worker.SessionRoomIO`, including recording/report hooks. Use `RoomOptions` for the supported settings, not direct Pion room access. `transport.DeleteRoomOnClose` is a separate opt-in property; it does not implement the custom RoomOptions deletion callback contract.

## Worker shutdown

After draining and closing every native session, call:

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := nativeroom.CloseInProcessRuntime(shutdownCtx); err != nil {
    // Report shutdown failure through the worker's logger/error result.
}
```

Arrange defer order so server/session cleanup happens before runtime disposal. Do not call this per session. Native is process-global, cannot coexist with another FFI owner, and cannot be reinitialized after disposal. A native crash still affects the worker process. Context bounds waiting; it cannot forcibly terminate a blocked native call.

## Use a local checkout downstream

From `agent-worker-golang`, use temporary module files to preserve its dependency files:

```sh
RTP_CHECKOUT=/Users/el7cosmos/src/cavos/rtp-agent
TEST_MOD_DIR="$(mktemp -d)"
cp go.mod "$TEST_MOD_DIR/go.mod"
cp go.sum "$TEST_MOD_DIR/go.sum"
export GOWORK=off

go mod edit -modfile="$TEST_MOD_DIR/go.mod" \
  -replace="github.com/cavos-io/rtp-agent=$RTP_CHECKOUT" \
  -replace="github.com/cavos-io/agent-worker-golang/pkg/hecttor-sdk=$PWD/pkg/hecttor-sdk"

go build -mod=mod -modfile="$TEST_MOD_DIR/go.mod" -tags nativeinprocess \
  -o "$TEST_MOD_DIR/agent-worker" ./cmd
```

**No `native-receive-probe` requirement/replacement is needed now.** The runtime moved into the root module. The local downstream checkout has an explicit native branch; its integration remains uncommitted. The build tag alone does not change runtime selection.

Keep the pinned FFI 0.12.80 library and platform checksum instructions from the [build guide](../../examples/starter/packaging/INPROCESS.md). Existing Opus/PortAudio, model and provider dependencies still apply. No Python packaging or probe setup is needed. Linux runtime/image validation is recorded below; live Linux media acceptance remains open.

## Import migration and fallback

- `examples/internal/nativeroom` moved to `interface/worker/livekit/nativeroom`.
- `native-receive-probe/nativeinprocess` moved to `interface/worker/livekit/nativeinprocess`. Applications should use the room facade; direct runtime consumers must respect its single-owner lifecycle.
- Shared `ffipb` stays at `interface/worker/livekit/ffipb`.
- `NewInProcess` and `CloseInProcessRuntime` remain behind the `nativeinprocess` tag. `New` keeps the helper-process implementation available without that tag.
- Starter and local-example imports were updated. Historical artifacts retain their original paths and commands; they describe the versions tested then.
- Default Pion selection and helper behavior are unchanged. Rollback remains drain/restart using the default or helper branch, not a live switch.

## Readiness and evidence

The external-module import restriction is resolved. A separate-module program builds with only the root-checkout replacement, constructs a SessionRoomIO-compatible native transport and calls shutdown without loading FFI. A public-package test validates configuration without opening native resources. Existing native, helper and starter regressions are moved with the implementation and run under the race detector.

The [original audit](native-downstream-compatibility.md) describes the pre-integration checkout. Its implementation blockers have since been addressed in the local native integration; implementation and unit-test coverage do not close the live acceptance gates below. Record-only mute retention was fixed previously. Do not promote native based on successful imports alone.

Run `scripts/test-native-public-api.sh` to repeat the separate-module import/configuration/shutdown check without loading a native library.

Evidence, including the initial worker-boundary placement failure, is preserved in `examples/native-receive-probe/results/public-native-api-20261117/` and [the manifest](../../examples/starter/NATIVE_PUBLIC_API_RESULTS.json).

## Supported options and embedded shutdown

Native now rejects explicit unsupported `RoomOptions` before acquiring media.
The supported fields are `DisablePreConnectAudio` (must be true),
`ParticipantIdentity`, `RecordRemoteParticipants`, `DisableTextInput`,
`TextInputCallback`, `DisableTranscriptionOutput`, and `TranscriptionNextOutput`.
All other fields must remain zero/nil/empty. This includes audio input/output
disable flags: session mute controls are separate and retain recording.
Native audio uses its existing fixed profile; specifying a custom rate, frame
size, processor or track name is rejected, even if the value resembles a default.
The helper and default Pion option handling are unchanged.

Embedded applications that own FFI should call `cli.RunAppWithOptionsError`.
It returns command errors after server closure, allowing application defers to
run. Dispose the native runtime only after successful server drain/closure, using
a fresh bounded context. `cli.RunAppWithOptions` preserves standalone process-exit
behavior for existing consumers. SIGKILL and a blocked native call cannot be
turned into graceful disposal by this API.

The downstream worker integration now supplies participant lifecycle, reliable
data and hold controls described in its `docs/native-transport.md`. The older
audit above is historical. Linux runtime/image checks are recorded in
[validation evidence](../results/native-linux-validation-20261118/README.md);
remaining live downstream acceptance still gates promotion. Pion remains the default.


## Hold diagnostics

With native enabled, `native.hold.published` confirms publication acknowledgement.
`native.hold.pcm_delivered` reports the first successful capture, then every 500
frames (about ten seconds for 20 ms frames), with frame count, PCM duration and
that frame's absolute peak amplitude. These counters measure native SDK
acknowledgements, not remote playback. `native.hold.stopped` reports final counts
and whether clearing succeeded. Publication, capture and clearing errors are
reported separately.

STG browser call `1117981` on 2026-11-18 published hold at 07:58:00 Asia/Jakarta,
delivered 1,470 frames / 29.4 seconds of PCM, then cleared successfully at
07:58:30 and played the transfer-unavailable message. The tester confirmed
hearing hold audio and intentionally did not answer the transferred call.
This covers audible browser hold and the unanswered-transfer path; it does not
establish an answered handoff for this run.

## Default-promotion validation checklist

Updated 2026-11-18 through upstream `fd60f7d99`, including teardown/report fixes
(`ebe3d436a`), assignment/logger fixes (`422761bb1`), and recursive race/environment
fixes (`fd60f7d99`). Evidence also includes the local uncommitted
agent-worker-golang integration, reviewed STG calls and tester observations.
**Native is not yet ready for an unrestricted default switch. Pion stays default
and remains the rollback transport.** This checklist supersedes the original
compatibility audit's outstanding-work list, not its historical evidence.

### Covered evidence

| Status | Scenario | Evidence and limits |
| --- | --- | --- |
| Passed | Linux packaging and idle lifecycle | [Linux results](../results/native-linux-validation-20261118/README.md): arm64 and emulated amd64 FFI initialization/disposal, runtime race suite, downstream image build, nonroot library loading, dependencies/assets, selector behavior and idle SIGTERM exit 0. No live Linux media was exercised. |
| Passed | Answered outbound SIP with native media | `1117978`: native room initialized; 30.82s input PCM; successful 33s call; agent `end_call` and proper closing persisted. Native handles released without callback overflow. Trace `a41247524918b2add270961fd1935cae`. |
| Passed | Rejected outbound invitation | `1117977`: expected rejection confirmed by tester; SIP 480, failed/0s outcome before native media started. Trace `c97f2dcb681941f1b522494ef287d180`. This validates rejection handling, not native media. |
| Passed, observed | Browser microphone and live captions | Native browser calls reached conversation/transfer; tester confirmed visible live captions. Tester also confirmed captions disabled with `DisableTranscriptionOutput=true` on the active in-process startup path; no frontend transcription was visible. |
| Passed, tester-confirmed | Text chat and interruption without duplicate replies | Tester confirmed both behaviors on 2026-11-18. Call IDs/revisions were not supplied for this confirmation; reliable `agent.events` correlation remains separate. |
| Passed for observed media; lifecycle follow-ups remain | Answered warm transfer | `1117979`: native caller transport, route to developer, supervisor briefing, record-only mode and transfer row 115 `DONE`; recording reference 84274. Trace `7b9192a97ca0a2961c9c67b60d660a90`. Tester confirmed two-way human audio both live and in the recording. Explicit agent-silence and hangup-propagation checks remain. |
| Passed | Audible browser hold and unanswered-transfer recovery | `1117981`: tester heard hold and intentionally did not answer the transferred call; publication acknowledged, 1,470 frames / 29.4s PCM delivered, clear succeeded, transfer-unavailable message played. No answered handoff was exercised in this run. |
| Partial | Recording and transcript persistence | `1117978` has audio reference 84270, summary and six persisted conversation rows; `1117979` has pre/post recording finalization evidence. Tester confirmed both human parties audible in the `1117979` recording. Ordinary-call playback and detailed pre/post continuity/duration checks remain. |
| Passed | Focused implementation checks | Native runtime/room/CLI race suites, default-build packages, architecture and commit hooks passed. Unsupported RoomOptions fail explicitly; embedded CLI returns after cleanup. These are not active-call drain or soak results. |
| Passed, local component scope | Active hold and forced drain | [Downstream local results](../results/native-downstream-local-20261118/README.md): two active calls, real SIGTERM, hold cleared before closure; five-second forced drain finalized reports and recordings without a detected report race. Both jobs ended, handles released, runtime disposed. Full app.Run/provider/storage finalizers were bypassed. |
| Passed | Recursive worker race and repeatability checks | [Final recursive results](../results/worker-recursive-races-20261118/README.md): complete worker subtree plus CLI passed with `-race -count=3`, both default and `nativeinprocess` builds, with repetitions in the same process per package. Agora/IPC races and credential leakage no longer reproduce in these runs. Architecture and commit hooks passed. |
| Passed, short observation | Two-call synthetic media isolation | [Downstream local results](../results/native-downstream-local-20261118/README.md): two-minute run, distinct caller tones, continued media during graceful drain, decodable recordings and no alien-dominant checked windows. This does not establish sustained RSS stability or caption/data isolation. |
| Passed, live local component scope | Native automated-test evidence delivery | [agent.events results](../results/native-agent-events-20261118/README.md): real LiveKit/native FFI, independent observer, readiness and two correlated chat turns from agent identity; duplicate replay without another generation; race-enabled run passed. Synthetic replies; dispatch and manager/UI workflow were bypassed. |

Calls `1117947`–`1117952` ran without the native flag and do not count toward
native acceptance. Transcript cleanup warnings are excluded from this checklist
at the tester's request. Per-session `runtime_disposed=false` is expected for the
process-owned runtime and is not a leak finding by itself.

### Additional local lifecycle evidence

[Local checks on 2026-11-18](../results/native-local-lifecycle-20261118/README.md)
passed with the real FFI against a loopback LiveKit server: stalled-room isolation,
three repeated live-session closes, runtime disposal during live audio,
pause/interruption, and recovery after a two-second signaling outage. Focused
hold/lifecycle/CLI race tests passed three repetitions. These narrow passes do not
close the downstream active-call SIGTERM or sustained concurrency gates below.

[Downstream-component checks](../results/native-downstream-local-20261118/README.md)
then passed a two-minute/two-call synthetic-audio run and actual SIGTERM with
natural call completion during drain, including decoded recording markers.
Initial hold and forced-drain runs found teardown ordering and report serialization
races. After fixes, both cases pass under the race detector: hold clears before
transport closure; forced drain completes in about five seconds with finalized
reports and decodable recordings. The worker logger/assignment race follow-up is
recorded in [worker race results](../results/worker-races-20261118/README.md);
the [recursive follow-up](../results/worker-recursive-races-20261118/README.md)
passes both recursive race suites three times, including Agora/IPC and repeated-run
environment isolation. The earlier failure logs are historical evidence, not open
blockers. The downstream fixture bypasses full app.Run dispatch/providers/storage
and is not a long soak.

The identified local hold teardown, forced-drain report, worker logger/assignment,
Agora/IPC and test environment defects are resolved. No known failure from those
checks remains open. Native promotion still requires the end-to-end and deployment
evidence below; passing local suites does not close those gates.

### Remaining gates

- [ ] **Validate the deployed automated-test workflow.** Local native `agent.events`
  delivery, sender identity, two-turn correlation and duplicate replay now pass
  with real LiveKit/FFI and synthetic replies. Run the actual manager/tester flow
  on the final artifact to cover dispatch and provider-backed replies.
  Ordinary web microphone, captions enabled/disabled, text chat and interruption
  without duplicate replies
  are tester-confirmed; attach call/revision identifiers when available. For a
  default profile including automated tests, verify the workflow consumes readiness,
  acknowledgements and completed transcripts with matching run_id/message_id/turn.
  This topic belongs to the downstream AutomatedTest integration, not the plain
  examples/local session.
  Run with transcription output enabled: the current integration feeds completed
  agent text through TranscriptionNextOutput, which caption disabling suppresses.
  Visible captions alone do not prove this separate data path.
- [ ] **Finish answered transfer and recording acceptance.** Two-way live and recorded human audio is confirmed for
  `1117979`, with a persisted DONE outcome. Verify agent silence in record-only
  mode and hangup propagation. Play an ordinary-call recording and inspect
  transferred pre/post ordering and duration for gaps or unexpected truncation. Repeat cold
  and hot transfer only if those modes are included in the intended default
  profile; `1117979` covers the warm control flow only. Secondary supervisor
  sessions remain Pion by design; do not require their migration for this gate.
- [ ] **Validate incoming SIP on a reachable worker.** Local inbound testing is
  currently unavailable. Use an environment that can receive the inbound route;
  verify answer, conversation, hangup and recording. This remains required for
  an unrestricted default that includes incoming calls.
- [ ] **Run live media on the deployment Linux architecture.** Use the final
  downstream image with native explicitly enabled against STG. Exercise web/SIP,
  hold/transfer and recording there. macOS calls plus Linux library smoke are not
  Linux media validation. If deployment is amd64, run on native amd64 hardware;
  the current amd64 image evidence comes from emulation.
- [ ] **Validate full-application shutdown and recovery.** Local component hold,
  natural drain and five-second forced drain pass. Run the real downstream app
  with providers and session-end persistence during speech and an active transfer;
  SIGTERM/drain must complete or terminate according to the configured drain
  policy, finalize recordings, release session handles and dispose FFI only after
  all sessions close. Also test network disconnect/reconnect, microphone
  republishing and remote participant departure: recovery or clean failure must
  preserve isolation, outcomes and bounded shutdown. Idle SIGTERM is already
  covered and need not be repeated as a substitute. Local two-second signaling
  recovery is also covered; verify downstream outcomes and recording continuity
  for these recovery cases on the final artifact.
- [ ] **Run sustained concurrent-call and repeated-call soak.** The two-call,
  two-minute media run and repeated unit suites pass. Extend to a defined sustained
  workload with distinct audio/chat markers; check no cross-call audio, captions,
  events or recordings, no callback overflow, stable PCM delivery, and no growing
  handle/goroutine/memory accumulation after repeated teardown. Record workload,
  duration and resource trends against Pion; define acceptable limits before
  calling the result a pass.
- [ ] **Confirm the supported default profile.** Inventory effective downstream
  options and accept the native fixed audio profile and explicitly disabled
  pre-connect audio. Unsupported configurations must remain on Pion or gain
  support before inclusion; rejecting them at runtime alone is not sufficient
  for a broad default. Agree which web, SIP and transfer modes the switch covers.
- [ ] **Release and validate the deployable artifact and rollback.** Review and
  commit downstream integration separately when authorized; release/pin the
  upstream revision containing all fixes through `fd60f7d99`, then build without
  the temporary sibling replacement. Earlier Linux images predate these fixes.
  Repeat relevant acceptance on that exact artifact. Verify
  drain/restart with explicit Pion selection and document the selector used for
  rollback after the default changes. Rollback is not an in-call transport swap.

For each new run, record call ID, transport-selection/initialization evidence,
trace ID where available, image/revision/architecture, expected outcome and
observed result. Mark an item passed only within the tested scope. Once these
gates pass for the agreed profile, make the default change as a separate reviewed
change with Pion retained and a bounded initial rollout.
