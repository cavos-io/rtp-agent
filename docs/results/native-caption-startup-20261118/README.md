# Startup caption investigation — 2026-11-18

The confirmed loss is in the soak caller's Go SDK participant lookup. Native
transport delivered the caption. The fixture sent its first chat input as soon
as agent.events/ready_for_tester arrived, although that data packet can precede
the caller's signaling update announcing native-agent.

A temporary SDK receive probe recorded the full expected final caption with
known=false immediately before OnTranscription discarded it. This reproduced
naturally twice in 26 calls. Injecting a 300 ms delay before caller participant
updates made all six immediate-start calls fail the same way. The SDK is
server-sdk-go/v2 v2.17.0; its room.go OnTranscription returns early for an unknown
transcribed participant. No publication retry or native startup delay fixes
that receiver ordering contract reliably.

## Fix

The corrected soak client registers OnParticipantConnected before connecting.
After receiving ready_for_tester, it checks whether native-agent is already
known; otherwise it waits for that identity's discovery event, bounded by a
10-second timeout. It sends the first lk.chat input only after discovery.
Other identities cannot release the wait. The current-state check also handles
an agent already present when the caller connects. This replaces both the
returned-audio barrier and the arbitrary Pion startup sleep.

The caption, event correlation, audio isolation and cleanup assertions are
unchanged. This is a validation-client correction. Native/Pion implementation,
server readiness semantics and dependency versions are unchanged. No SDK fork
is shipped. A production client that needs to consume captions before discovery
must buffer them itself or use a receiver that supports that ordering; this
fixture cannot establish real frontend behavior.

## Verification

| Check | Result |
| --- | --- |
| Natural immediate-input reproduction, SDK receive probe only | FAIL: 2/26 captions dropped for unknown participant (133.00s) |
| Immediate input with 300 ms participant-update delay | FAIL: 6/6 captions dropped for unknown participant (31.42s) |
| Corrected participant-ready client, same injected delay | PASS: 26/26 calls, all incoming captions known; audio/events/captions/cleanup assertions intact (132.10s) |
| Corrected client, stock SDK, Go race detector | PASS: 6/6 calls, no detected races (36.91s) |
| Corrected client, Pion, injected delay | PASS: 6/6 calls (29.90s) |

The confirmed failure is fixed within the local validation client's scope. The
original native publisher required no change. The ten-minute media measurement
from the prior run remains valid; it was not rerun because only receiver startup
ordering changed. Longer deployment/provider/recording validation remains open.

All runs use the same loopback server and
synthetic credentials as the preceding soak. The receive probe and injected
delay exist only in a temporary SDK copy and modfile. The race check uses the
unmodified SDK. Downstream source files and commits are untouched. The isolated server and all
validation workers were stopped after the runs.

## Reproduction

Use the same helper and evidence overlays as
[the original soak](../native-sustained-soak-20261118/README.md), replacing only
local_native_soak_test.go with participant_ready_soak_test.go.txt in this folder.
SOAK_REPEAT_ONLY=1 runs six seconds plus twelve two-call eight-second waves;
SOAK_SMOKE=1 runs only two short waves after the initial six-second pair.
SOAK_PION=1 selects the default session transport for comparison. Without these
short-run flags, the corrected fixture retains the original ten-minute phase.

For the receive diagnostic, copy the pinned Go SDK outside GOMODCACHE and apply
sdk-diagnostic.patch to that copy with git apply --unidiff-zero. It prints incoming transcription text and
whether the participant is known, and delays new native-agent updates by 300 ms.
Use this only with synthetic test text. Point a temporary downstream modfile's
SDK replacement at the copy; set GOWORK=off when building with -modfile. Never
modify the module cache or ship this diagnostic dependency. For the natural
reproduction, omit the delay hunk while retaining the receive probe.

The old immediate-input fixture is preserved in the preceding soak evidence;
run it with SOAK_SMOKE=1 for the forced failure. Build and execution commands
otherwise match that report. The regression runs change only caller readiness
between the failing and passing variants. Raw files and binaries remain in
/private/tmp/native-caption-20261118; extracted assertions, wire observations
and hashes accompany this report. Original failed evidence is retained.
