# Linux live media — 2026-11-18

Result: **PASS for local Linux component scope**, 18 successful runs. Pion remains
default; downstream files remain unchanged and uncommitted. Tests use synthetic
credentials and no STG calls. The test server/network were removed afterward.

## Results

| Check | Linux arm64 | Linux amd64, emulated |
| --- | --- | --- |
| Two simultaneous calls, 13 waves / 26 sessions, separate caller processes | PASS, 134.80s | PASS |
| Distinct duplex audio, chat, final captions and agent.events correlation | PASS | PASS |
| Caption packets and lk.transcription text streams | PASS | PASS |
| Playback drain/clear, false-interruption resume and caller departure | PASS | PASS |
| Delayed subscription, mute/unmute and microphone device republishing | PASS | PASS |
| Agent/user state publication with appropriate token grants | PASS | PASS |
| DTMF receipt and end-call control | PASS | PASS |
| Two-second signaling outage and recovery | PASS | PASS |
| Active hold publication/PCM/clear, two-call graceful SIGTERM | PASS, 2.086s drain | PASS, 2.155s drain |
| Two-call forced SIGTERM drain | PASS, 5.022s | PASS, 5.165s |
| MP4/AAC recordings after hold and forced drain | 4/4 decode and marker checks pass | 4/4 decode and marker checks pass |
| Per-session handle cleanup in repeated-call run | 26/26 | 26/26 |

After each short-wave cleanup, arm64 RSS changed from 110.59 to 111.82 MiB,
Go heap from 7.37 to 7.46 MiB and goroutines stayed at 12. Emulated amd64 RSS
changed from 194.98 to 195.81 MiB, heap from 8.40 to 7.59 MiB and goroutines
ranged 14–15. Both runs met the existing +32 MiB RSS, +16 MiB heap and +10
goroutine bounds. These short repeated-call checks are not a long Linux soak.
No cross-call tone markers were detected in checked input/returned-audio frames,
and all correlated text/caption/event assertions passed. Native runtime close
returned successfully in the repeat and drain fixtures.

All eight recordings were decoded on the host with ffmpeg. Interior one-second
windows contained the expected room tone, with zero alien-dominant windows.
This checks decodability, duration and isolation; it does not establish human
transfer pre/post continuity or perceived hold-music quality. Recording details
are in recording-analysis.json; MP4 hashes and raw paths are in manifest.json.

Some drain/reconnect fixtures intentionally use tokens without self-metadata
permission and log NotAllowed state-publication warnings. The separate state
fixture supplies that permission and verifies attributes/events successfully.
The forced-drain timeout warning is the expected stimulus, not a failed run.

## Scope

Current source is compiled into test executables with ffmpeg,nativeinprocess.
Executables run as the existing downstream image's appuser, using that image's
FFI 0.12.80, FFmpeg shared libraries and hold asset. This is a test image derived
from the validated runtime layer, **not a newly released full application image**.
The production main executable in that base image is not used by these checks.

A Linux LiveKit 1.13.11 container uses a dedicated internal Docker network with
no external connectivity. Test containers share its namespace, signal over
loopback and exchange actual media through its internal interface.
The host Docker engine is arm64. amd64 runs use emulation, not amd64 hardware.

Downstream component tests bypass app.Run dispatch, real STT/LLM/TTS providers,
SIP routing and database/storage finalizers. The corrected caller waits for agent
participant discovery before first synthetic chat input. The repeated-call test
uses separate caller processes and retains audio/caption/event isolation checks.
The drain fixtures exercise actual SIGTERM, session recording and native hold.

The slim image does not include ps. A test-only executable mounted at
/usr/local/bin/ps accepts only the fixture's `ps -o rss= -p PID` invocation and
reads VmRSS from /proc/PID/status. This reports worker process RSS, not native
allocations alone. No runtime image package is installed for sampling.

## Build provenance

Build contexts contain current tracked/untracked Go implementation, required C
sources/headers, module files and explicit assets only. No dotenv, repository
credentials or unrelated working files are copied. Existing GitHub credentials
are supplied only as a BuildKit secret to resolve private dependencies.

Initial test-image builds unnecessarily entered provider asset download; those
were canceled. The narrowed build reuses final-image assets. A second attempt
failed because the filtered context omitted C headers; adding the required C
sources and headers corrected that context error. Failed/canceled build logs
remain under /private/tmp/native-linux-live-20261118. The initial loopback-only
namespace also failed caller ICE setup before native startup. Adding an internal
interface resolved this harness issue; all corresponding reruns passed. Neither
setup failure required a product change.

## Remaining limits

Even successful local Linux component checks do not close final-artifact STG
web/SIP/transfer/provider workflows, recording handoff continuity, production
persistence/drain, incoming SIP, native amd64 hardware validation, or deployment
soak. Release/pinning and rollback remain separate promotion gates.


## Reproduction and evidence

Copy the provided build/run scripts, Dockerfile, ps wrapper and livekit.yaml to
/private/tmp/native-linux-live-20261118 first, and make ps executable.
Dockerfile is the exact test-image build definition. snapshot.py copies only
source/module inputs and explicit assets into fresh temporary contexts, then
adds the preserved downstream test fixtures. build.py builds each architecture
with a BuildKit credential secret. Images derive from the prior validated
agent-worker-validation:<arch>-native-true runtime layers, so retain those images
or rebuild their documented equivalents first. The named upstream context is
required while the downstream checkout uses a sibling module replacement.

Create an internal Docker network and start livekit/livekit-server:v1.13.11 as
native-linux-live-server using livekit.yaml. run.py <arm64|amd64> <case> executes
the cases in the result table; hold/forced runs must be sequential because their
worker health port is shared. repeat and starter cases can run independently.
The ps wrapper is mounted read-only for process RSS sampling. All test runs use
the final layer's appuser (UID 10001). Image IDs, library hashes and linker output
are retained. Both test images have no native runtime-selection flag in their
configuration; fixtures explicitly opt in.

Logs contain extracted test assertions, cleanup/hold diagnostics and peer
summaries. measurements/ preserves JSON metrics, state/caption observations and
drain summaries. Raw logs and MP4s remain under /private/tmp/native-linux-live-20261118.
No images were pushed, deployment settings changed or source fixes made.
